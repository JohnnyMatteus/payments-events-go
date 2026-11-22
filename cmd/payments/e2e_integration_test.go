//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/httpapi"
	"github.com/JohnnyMatteus/payments-events-go/internal/testinfra"
)

const e2eSecret = "whsec_e2e_dev_only"

var (
	pg         *testinfra.Postgres
	amqpURL    string
	s3Endpoint string
)

func TestMain(m *testing.M) {
	os.Exit(testinfra.Run(m, func(ctx context.Context) (func(), error) {
		var (
			stopPG, stopMQ, stopS3 func()
			err                    error
		)
		if pg, stopPG, err = testinfra.StartPostgres(ctx); err != nil {
			return nil, err
		}
		if amqpURL, stopMQ, err = testinfra.StartRabbitMQ(ctx); err != nil {
			stopPG()
			return nil, err
		}
		if s3Endpoint, stopS3, err = testinfra.StartLocalStack(ctx); err != nil {
			stopMQ()
			stopPG()
			return nil, err
		}
		return func() { stopS3(); stopMQ(); stopPG() }, nil
	}))
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

// TestEndToEnd sobe api, relay e consumidor pelo mesmo caminho do binário
// e verifica o fluxo completo: webhook assinado -> ledger + outbox ->
// RabbitMQ -> projeção idempotente -> conciliação com divergências.
func TestEndToEnd(t *testing.T) {
	apiAddr := freeAddr(t)
	env := map[string]string{
		"DATABASE_URL":         pg.DSN,
		"RABBITMQ_URL":         amqpURL,
		"WEBHOOK_SECRETS":      e2eSecret,
		"HTTP_ADDR":            apiAddr,
		"OUTBOX_POLL_INTERVAL": "50ms",
		"LOG_LEVEL":            "info",
		"S3_ENDPOINT":          s3Endpoint,
		"S3_USE_PATH_STYLE":    "true",
	}
	getenv := func(extra map[string]string) func(string) string {
		return func(k string) string {
			if v, ok := extra[k]; ok {
				return v
			}
			return env[k]
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	codes := map[string]*int{}
	logs := map[string]*syncBuffer{}
	start := func(name string, extra map[string]string) {
		code := new(int)
		codes[name] = code
		out := &syncBuffer{}
		logs[name] = out
		wg.Go(func() { *code = run(ctx, []string{name}, getenv(extra), out, out) })
	}
	// Registrado antes da parada dos processos: o Cleanup roda em ordem inversa,
	// então o diagnóstico sai depois de api, relay e consumidor terminarem e com
	// a saída deles completa.
	t.Cleanup(func() {
		if t.Failed() {
			dumpDiagnostics(t, logs)
		}
	})
	t.Cleanup(func() {
		cancel()
		wg.Wait()
		for name, code := range codes {
			if *code != exitOK {
				t.Errorf("%s exited with code %d", name, *code)
			}
		}
	})

	// Os três processos dividem o mesmo binário de teste e, portanto, os
	// providers globais do OpenTelemetry. A API sobe primeiro para que suas
	// métricas fiquem no próprio registry antes dos outros processos.
	base := "http://" + apiAddr
	start("api", nil)
	waitFor(t, "api readiness", func() bool {
		resp, err := http.Get(base + "/readyz")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})
	start("relay", map[string]string{"OPS_ADDR": freeAddr(t)})
	start("consumer", map[string]string{"OPS_ADDR": freeAddr(t)})

	capture := webhookBody("evt_e2e_1", "payment.captured", "txn_e2e_1", 15000)
	refund := webhookBody("evt_e2e_2", "payment.refunded", "txn_e2e_2", 2500)
	for _, step := range []struct {
		body       string
		wantStatus string
	}{
		{capture, "recorded"},
		{refund, "recorded"},
		{capture, "duplicate"}, // reentrega do provedor
	} {
		if got := postWebhook(t, base, step.body); got != step.wantStatus {
			t.Fatalf("webhook status = %q, want %q", got, step.wantStatus)
		}
	}

	waitFor(t, "balance projection", func() bool {
		var captured, refunded int64
		err := pg.Pool.QueryRow(context.Background(),
			"SELECT captured_cents, refunded_cents FROM payment_balances WHERE payment_id = 'pay_e2e'").
			Scan(&captured, &refunded)
		return err == nil && captured == 15000 && refunded == 2500
	})
	var pending int
	if err := pg.Pool.QueryRow(context.Background(),
		"SELECT count(*) FROM outbox_messages WHERE status <> 'published'").Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("outbox not drained: pending=%d err=%v", pending, err)
	}

	resp, err := http.Get(base + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	metrics, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(metrics), `outcome="duplicate"} 1`) {
		t.Fatalf("metrics missing duplicate outcome:\n%s", metrics)
	}

	// Extrato no S3 (LocalStack): txn_e2e_1 confere, txn_e2e_2 com valor diferente, txn_e2e_3 não está no ledger.
	csv := "transaction_id,payment_id,type,amount,currency,occurred_at\n" +
		"txn_e2e_1,pay_e2e,capture,150.00,BRL,2026-11-20T12:00:00Z\n" +
		"txn_e2e_2,pay_e2e,refund,30.00,BRL,2026-11-20T12:00:00Z\n" +
		"txn_e2e_3,pay_e2e,capture,10.00,BRL,2026-11-20T15:00:00Z\n"
	statement := uploadStatement(t, "settlements/2026-11-20.csv", csv)
	var out bytes.Buffer
	code := run(context.Background(),
		[]string{"reconcile", "-statement", statement, "-from", "2026-11-20", "-format", "json", "-fail-on-divergence"},
		getenv(nil), &out, io.Discard)
	if code != exitDivergence {
		t.Fatalf("reconcile exit code = %d, want %d; output %s", code, exitDivergence, out.String())
	}
	var report struct {
		Matched int            `json:"matched"`
		Counts  map[string]int `json:"divergence_counts"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("report is not JSON: %v\n%s", err, out.String())
	}
	if report.Matched != 1 || report.Counts["amount_mismatch"] != 1 || report.Counts["missing_in_ledger"] != 1 {
		t.Fatalf("report = %+v", report)
	}
}

func webhookBody(id, typ, tx string, cents int64) string {
	return fmt.Sprintf(`{"id":%q,"type":%q,"created_at":"2026-11-20T12:00:00Z",`+
		`"data":{"transaction_id":%q,"payment_id":"pay_e2e","amount":%d,"currency":"BRL"}}`, id, typ, tx, cents)
}

func postWebhook(t *testing.T, base, body string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+httpapi.WebhookPath, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(httpapi.SignatureHeader, httpapi.Sign([]byte(e2eSecret), time.Now(), []byte(body)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response (status %d): %v", resp.StatusCode, err)
	}
	return out["status"]
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func uploadStatement(t *testing.T, key, body string) string {
	t.Helper()
	ctx := context.Background()
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(s3Endpoint)
		o.UsePathStyle = true
	})
	const bucket = "pagafacil-statements"
	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: strings.NewReader(body),
	}); err != nil {
		t.Fatal(err)
	}
	return "s3://" + bucket + "/" + key
}

// syncBuffer recebe a saída de vários processos (goroutines) sem corrida.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// scrub tira do texto os valores sensíveis do ambiente de teste antes de logar.
func scrub(text string) string {
	for _, secret := range []string{pg.DSN, amqpURL, e2eSecret, s3Endpoint} {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[redigido]")
		}
	}
	return text
}

// dumpDiagnostics mostra, só quando o teste falha, o que cada processo escreveu
// e o estado do pipeline (outbox, projeção, dead letters e filas), para a falha
// dizer a causa em vez de só "timed out". Não imprime env, DSN nem segredos.
func dumpDiagnostics(t *testing.T, logs map[string]*syncBuffer) {
	t.Helper()
	for _, name := range []string{"api", "relay", "consumer"} {
		if b, ok := logs[name]; ok {
			t.Logf("saída de %s:\n%s", name, scrub(b.String()))
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rows, err := pg.Pool.Query(ctx, `
		SELECT id, message_id, routing_key, status, attempts, COALESCE(last_error, ''), next_attempt_at, published_at IS NOT NULL
		FROM outbox_messages ORDER BY id`)
	if err != nil {
		t.Logf("diagnóstico: consulta ao outbox falhou: %v", err)
	} else {
		defer rows.Close()
		for rows.Next() {
			var (
				id, attempts           int64
				msgID, key, st, lastEr string
				next                   time.Time
				published              bool
			)
			if err := rows.Scan(&id, &msgID, &key, &st, &attempts, &lastEr, &next, &published); err != nil {
				t.Logf("diagnóstico: leitura do outbox falhou: %v", err)
				break
			}
			t.Logf("outbox id=%d message_id=%s routing_key=%s status=%s attempts=%d next_attempt_at=%s published=%t last_error=%q",
				id, msgID, key, st, attempts, next.UTC().Format(time.RFC3339Nano), published, scrub(lastEr))
		}
	}

	var events, ledger, consumed, balances, deadLetters int
	err = pg.Pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM processed_events), (SELECT count(*) FROM ledger_entries),
		(SELECT count(*) FROM consumed_messages), (SELECT count(*) FROM payment_balances),
		(SELECT count(*) FROM dead_letters)`).Scan(&events, &ledger, &consumed, &balances, &deadLetters)
	if err != nil {
		t.Logf("diagnóstico: contagens falharam: %v", err)
	} else {
		t.Logf("contagens: processed_events=%d ledger_entries=%d consumed_messages=%d payment_balances=%d dead_letters=%d",
			events, ledger, consumed, balances, deadLetters)
	}
	var captured, refunded int64
	if err := pg.Pool.QueryRow(ctx,
		"SELECT captured_cents, refunded_cents FROM payment_balances WHERE payment_id = 'pay_e2e'").Scan(&captured, &refunded); err != nil {
		t.Logf("saldo de pay_e2e: %v", err)
	} else {
		t.Logf("saldo de pay_e2e: captured_cents=%d refunded_cents=%d (esperado 15000 e 2500)", captured, refunded)
	}

	conn, err := amqp.Dial(amqpURL)
	if err != nil {
		t.Logf("diagnóstico: conexão com o broker falhou: %s", scrub(err.Error()))
		return
	}
	defer func() { _ = conn.Close() }()
	for _, queue := range []string{"balance-projector", "balance-projector.dlq"} {
		ch, err := conn.Channel()
		if err != nil {
			t.Logf("diagnóstico: canal para %s falhou: %v", queue, err)
			return
		}
		q, err := ch.QueueDeclarePassive(queue, true, false, false, false, nil)
		if err != nil {
			t.Logf("fila %s: %v", queue, err)
			continue
		}
		t.Logf("fila %s: messages=%d consumers=%d", queue, q.Messages, q.Consumers)
		_ = ch.Close()
	}
}
