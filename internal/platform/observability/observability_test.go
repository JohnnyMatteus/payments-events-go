package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
)

func TestLoggerAddsTraceContext(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, "info", "test-service")

	tel, err := Setup(context.Background(), "test-service", "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tel.Shutdown(context.Background()) })

	ctx, span := otel.Tracer("test").Start(context.Background(), "op")
	logger.With("component", "x").InfoContext(ctx, "hello")
	logger.DebugContext(ctx, "filtered by level")
	span.End()

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected one log line, got %d: %s", len(lines), buf.String())
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["trace_id"] != span.SpanContext().TraceID().String() || entry["service"] != "test-service" || entry["component"] != "x" {
		t.Fatalf("log entry = %v", entry)
	}
}

func TestMetricsEndpointExposesRelayMetrics(t *testing.T) {
	tel, err := Setup(context.Background(), "test-service", "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tel.Shutdown(context.Background()) })

	m, err := NewRelayMetrics()
	if err != nil {
		t.Fatal(err)
	}
	m.Published(150 * time.Millisecond)
	m.Retried(1)
	m.DeadLettered()

	rec := httptest.NewRecorder()
	tel.MetricsHandler.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	for _, want := range []string{"outbox_published_total", "outbox_retries_total", "outbox_dead_total",
		"outbox_publish_lag_seconds_bucket", "go_goroutines"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("/metrics missing %s", want)
		}
	}
}

func scrape(t *testing.T, tel *Telemetry) string {
	t.Helper()
	rec := httptest.NewRecorder()
	tel.MetricsHandler.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	return string(body)
}

// A primeira mensagem morta precisa ser visível para o Prometheus: a série
// existe em 0 desde o início, antes de qualquer Add(1).
func TestRelayCountersExistAtZeroBeforeAnyEvent(t *testing.T) {
	tel, err := Setup(context.Background(), "test-service", "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tel.Shutdown(context.Background()) })
	if _, err := NewRelayMetrics(); err != nil {
		t.Fatal(err)
	}
	body := scrape(t, tel)
	for _, name := range []string{"outbox_published_total", "outbox_retries_total", "outbox_dead_total"} {
		if !regexp.MustCompile(`(?m)^` + name + `(\{[^}]*\})? 0$`).MatchString(body) {
			t.Errorf("%s deve aparecer em 0 antes do primeiro evento:\n%s", name, body)
		}
	}
}

func TestPipelineGaugesPublishSnapshotAndSkipOnReadFailure(t *testing.T) {
	tel, err := Setup(context.Background(), "test-service", "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tel.Shutdown(context.Background()) })
	fail := false
	err = RegisterPipelineGauges(func(context.Context) (PipelineSnapshot, error) {
		if fail {
			return PipelineSnapshot{}, io.ErrUnexpectedEOF
		}
		return PipelineSnapshot{DeadLettersOpen: 2, OpenCases: 3, OutboxPending: 1, LastReconciliationUnix: 1763800000}, nil
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	body := scrape(t, tel)
	for _, want := range []string{`pipeline_dead_letters_open`, `reconciliation_open_cases`, `reconciliation_last_run_timestamp_seconds`} {
		if !regexp.MustCompile(`(?m)^` + want + `(\{[^}]*\})? [0-9.e+]+$`).MatchString(body) {
			t.Errorf("faltou %s:\n%s", want, body)
		}
	}
	if !regexp.MustCompile(`(?m)^pipeline_dead_letters_open(\{[^}]*\})? 2$`).MatchString(body) {
		t.Errorf("pipeline_dead_letters_open deveria valer 2:\n%s", body)
	}
	fail = true
	if strings.Contains(scrape(t, tel), "pipeline_dead_letters_open ") {
		t.Error("com a leitura falhando, o gauge não deve repetir um valor velho")
	}
}
