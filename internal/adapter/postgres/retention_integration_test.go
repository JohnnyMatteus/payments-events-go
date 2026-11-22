//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/postgres"
	"github.com/JohnnyMatteus/payments-events-go/internal/app"
	"github.com/JohnnyMatteus/payments-events-go/internal/testinfra"
)

// ageEvent faz o evento parecer antigo. processed_events aceita UPDATE; o ledger não.
func ageEvent(t *testing.T, eventID string, at time.Time) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		"UPDATE processed_events SET received_at = $2 WHERE event_id = $1", eventID, at); err != nil {
		t.Fatal(err)
	}
}

func TestLedgerCannotBeTruncatedEvenThroughTheForeignKey(t *testing.T) {
	testinfra.ResetDatabase(t, pool)
	ingest(t, capture("evt_1", "txn_1", 100))
	ctx := context.Background()
	for _, stmt := range []string{"TRUNCATE ledger_entries", "TRUNCATE processed_events CASCADE"} {
		if _, err := pool.Exec(ctx, stmt); err == nil {
			t.Errorf("%s deve ser recusado", stmt)
		}
	}
	if count(t, "ledger_entries") != 1 || count(t, "processed_events") != 1 {
		t.Fatal("o razão e a chave de idempotência têm de continuar lá")
	}
}

func TestLedgerIsAppendOnlyInTheDatabase(t *testing.T) {
	testinfra.ResetDatabase(t, pool)
	ingest(t, capture("evt_1", "txn_1", 100))
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "UPDATE ledger_entries SET amount_cents = 1"); err == nil {
		t.Fatal("UPDATE em ledger_entries deve ser recusado")
	}
	if _, err := pool.Exec(ctx, "DELETE FROM ledger_entries"); err == nil {
		t.Fatal("DELETE em ledger_entries deve ser recusado")
	}
	if count(t, "ledger_entries") != 1 {
		t.Fatal("o lançamento tem de continuar lá")
	}
}

func TestArchiveEventPayloadsKeepsIdempotencyAndLedger(t *testing.T) {
	testinfra.ResetDatabase(t, pool)
	ctx := context.Background()
	ingest(t, capture("evt_old", "txn_old", 15000))
	ingest(t, capture("evt_new", "txn_new", 200))
	old := time.Now().AddDate(0, -3, 0).UTC()
	ageEvent(t, "evt_old", old)

	repo := postgres.NewRetention(pool)
	n, err := repo.ArchiveEventPayloads(ctx, time.Now().AddDate(0, 0, -30), 100)
	if err != nil || n != 1 {
		t.Fatalf("archived = %d, %v, want 1", n, err)
	}
	if again, err := repo.ArchiveEventPayloads(ctx, time.Now().AddDate(0, 0, -30), 100); err != nil || again != 0 {
		t.Fatalf("second run = %d, %v, want 0", again, err)
	}

	// A linha continua: a garantia de idempotência não depende do payload.
	if count(t, "processed_events") != 2 || count(t, "ledger_entries") != 2 {
		t.Fatalf("events=%d ledger=%d, want 2 and 2", count(t, "processed_events"), count(t, "ledger_entries"))
	}
	if got := ingest(t, capture("evt_old", "txn_old", 15000)); got != app.OutcomeDuplicate {
		t.Fatalf("redelivery of an archived event = %s, want duplicate", got)
	}
	if count(t, "ledger_entries") != 2 || count(t, "outbox_messages") != 2 {
		t.Fatal("a reentrega não pode gravar de novo")
	}

	var archivedPayload, livePayload string
	if err := pool.QueryRow(ctx, `SELECT payload::text FROM processed_events_archive WHERE event_id = 'evt_old'`).Scan(&archivedPayload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(archivedPayload, `"evt_old"`) {
		t.Fatalf("o arquivo deve guardar o payload completo, got %s", archivedPayload)
	}
	if err := pool.QueryRow(ctx, `SELECT payload::text FROM processed_events WHERE event_id = 'evt_old'`).Scan(&livePayload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(livePayload, `"evt_old"`) {
		t.Fatalf("a linha quente deve ter só o resumo, got %s", livePayload)
	}

	trail, err := postgres.NewOperationsReader(pool).EventTrail(ctx, "evt_old")
	if err != nil || !strings.Contains(string(trail.Payload), `"evt_old"`) {
		t.Fatalf("a trilha deve ler o payload do arquivo: %s, %v", trail.Payload, err)
	}
}

func TestPurgePublishedOutboxKeepsEverythingElse(t *testing.T) {
	testinfra.ResetDatabase(t, pool)
	ctx := context.Background()
	for _, id := range []string{"evt_pub_old", "evt_pub_new", "evt_pending", "evt_dead"} {
		ingest(t, capture(id, "txn_"+id, 100))
	}
	old := time.Now().AddDate(0, 0, -30)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE outbox_messages SET status = 'published', published_at = $1 WHERE message_id = 'evt_pub_old'`, old)
	exec(`UPDATE outbox_messages SET status = 'published', published_at = now() WHERE message_id = 'evt_pub_new'`)
	exec(`UPDATE outbox_messages SET status = 'dead' WHERE message_id = 'evt_dead'`)

	n, err := postgres.NewRetention(pool).PurgePublishedOutbox(ctx, time.Now().AddDate(0, 0, -7), 100)
	if err != nil || n != 1 {
		t.Fatalf("purged = %d, %v, want 1", n, err)
	}
	var left []string
	rows, err := pool.Query(ctx, "SELECT message_id FROM outbox_messages ORDER BY message_id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		left = append(left, id)
	}
	if strings.Join(left, ",") != "evt_dead,evt_pending,evt_pub_new" {
		t.Fatalf("outbox = %v", left)
	}
}

type memArchive struct{ objects map[string][]byte }

func (m *memArchive) PutArchive(_ context.Context, key string, body []byte) (string, error) {
	m.objects[key] = body
	return "mem://" + key, nil
}

// O ponto de entrada do job: política, repositório real e escritor em memória.
func TestRetentionRunExportsClosedLedgerMonthsWithoutTouchingTheLedger(t *testing.T) {
	testinfra.ResetDatabase(t, pool)
	ctx := context.Background()
	ingest(t, capture("evt_live", "txn_live", 700))
	// Lançamentos de dois meses distintos, gravados direto com recorded_at
	// explícito (o ledger não aceita UPDATE para envelhecer linhas).
	for i, at := range []time.Time{
		time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC),
	} {
		id := "evt_hist_" + string(rune('a'+i))
		if _, err := pool.Exec(ctx, `INSERT INTO processed_events (event_id, event_type, outcome, payload, received_at)
			VALUES ($1, 'payment.captured', 'recorded', '{}', $2)`, id, at); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO ledger_entries (event_id, transaction_id, payment_id, direction,
			amount_cents, currency, occurred_at, recorded_at)
			VALUES ($1, $2, 'pay_h', 'credit', $3, 'BRL', $4, $4)`, id, "txn_"+id, 1000+int64(i), at); err != nil {
			t.Fatal(err)
		}
	}
	before := count(t, "ledger_entries")

	repo := postgres.NewRetention(pool)
	archive := &memArchive{objects: map[string][]byte{}}
	svc, err := app.NewRetentionService(app.RetentionPolicy{
		OutboxPublishedAfter: 7 * 24 * time.Hour, EventPayloadsAfter: 30 * 24 * time.Hour,
		LedgerExportAfter: 90 * 24 * time.Hour, BatchSize: 100,
	}, repo, repo, archive, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	rep, err := svc.Run(ctx, time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Join(rep.LedgerMonthsArchived, ",") != "2026-06,2026-07" {
		t.Fatalf("months = %v, want 2026-06 and 2026-07 (o mês atual não está fechado)", rep.LedgerMonthsArchived)
	}
	if lines := strings.Count(string(archive.objects["ledger/2026-06.csv"]), "\n"); lines != 3 { // cabeçalho + 2
		t.Fatalf("2026-06.csv has %d lines, want 3", lines)
	}
	if count(t, "ledger_entries") != before {
		t.Fatal("exportar não pode alterar o ledger")
	}
	var rowCount int64
	if err := pool.QueryRow(ctx, "SELECT row_count FROM ledger_archives WHERE month = '2026-06-01'").Scan(&rowCount); err != nil || rowCount != 2 {
		t.Fatalf("ledger_archives = %d, %v", rowCount, err)
	}
	again, err := svc.Run(ctx, time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC))
	if err != nil || len(again.LedgerMonthsArchived) != 0 {
		t.Fatalf("second run = %+v, %v, want no months", again, err)
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// publishAndAge marca a mensagem como publicada há muito tempo.
func publishAndAge(t *testing.T, messageID string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE outbox_messages SET status = 'published', published_at = now() - interval '30 days' WHERE message_id = $1`,
		messageID); err != nil {
		t.Fatal(err)
	}
}

func archiveDeadLetter(t *testing.T, messageID string) {
	t.Helper()
	var key, body string
	if err := pool.QueryRow(context.Background(),
		"SELECT routing_key, payload::text FROM outbox_messages WHERE message_id = $1", messageID).Scan(&key, &body); err != nil {
		t.Fatal(err)
	}
	err := postgres.NewDeadLetterRepository(pool).Archive(context.Background(), app.ArchivedMessage{
		MessageID: messageID, RoutingKey: key, Payload: []byte(body), Reason: "delivery_limit", Deliveries: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func openDeadLetter(t *testing.T, messageID string) app.DeadLetter {
	t.Helper()
	list, err := postgres.NewOperationsReader(pool).ListDeadLetters(context.Background(), "open", messageID)
	if err != nil || len(list) != 1 {
		t.Fatalf("dead letters of %s = %+v, %v", messageID, list, err)
	}
	return list[0]
}

// A mensagem com dead letter aberta não é apagada: o redrive precisa dela.
func TestPurgeKeepsMessagesWithOpenDeadLetter(t *testing.T) {
	testinfra.ResetDatabase(t, pool)
	ctx := context.Background()
	ingest(t, capture("evt_dl", "txn_dl", 100))
	publishAndAge(t, "evt_dl")
	archiveDeadLetter(t, "evt_dl")

	n, err := postgres.NewRetention(pool).PurgePublishedOutbox(ctx, time.Now().AddDate(0, 0, -7), 100)
	if err != nil || n != 0 || count(t, "outbox_messages") != 1 {
		t.Fatalf("purged = %d, %v, outbox = %d, want 0 and 1", n, err, count(t, "outbox_messages"))
	}
	dl := openDeadLetter(t, "evt_dl")
	if err := postgres.NewDeadLetterRepository(pool).Requeue(ctx, dl.Source, dl.ID, "ana", "banco voltou"); err != nil {
		t.Fatalf("Requeue: %v", err)
	}
}

// A dead letter que chega depois da limpeza também pode ser reprocessada: a
// mensagem é recriada pendente, com o mesmo message_id.
func TestRedriveWorksAfterTheOutboxRowWasPurged(t *testing.T) {
	testinfra.ResetDatabase(t, pool)
	ctx := context.Background()
	ingest(t, capture("evt_late", "txn_late", 100))
	publishAndAge(t, "evt_late")
	// Guarda a mensagem como o consumidor da DLQ a veria, antes de purgar.
	var key, body string
	if err := pool.QueryRow(ctx, "SELECT routing_key, payload::text FROM outbox_messages WHERE message_id = 'evt_late'").Scan(&key, &body); err != nil {
		t.Fatal(err)
	}

	n, err := postgres.NewRetention(pool).PurgePublishedOutbox(ctx, time.Now().AddDate(0, 0, -7), 100)
	if err != nil || n != 1 || count(t, "outbox_messages") != 0 {
		t.Fatalf("purged = %d, %v, outbox = %d, want 1 and 0", n, err, count(t, "outbox_messages"))
	}
	err = postgres.NewDeadLetterRepository(pool).Archive(ctx, app.ArchivedMessage{
		MessageID: "evt_late", RoutingKey: key, Payload: []byte(body), Reason: "delivery_limit", Deliveries: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	dl := openDeadLetter(t, "evt_late")
	if !dl.HasOutbox {
		t.Fatal("a dead letter de uma mensagem purgada deve poder ser reprocessada (has_outbox)")
	}
	if err := postgres.NewDeadLetterRepository(pool).Requeue(ctx, dl.Source, dl.ID, "ana", "banco voltou"); err != nil {
		t.Fatalf("Requeue after purge: %v", err)
	}
	var status, routingKey string
	if err := pool.QueryRow(ctx, "SELECT status, routing_key FROM outbox_messages WHERE message_id = 'evt_late'").Scan(&status, &routingKey); err != nil {
		t.Fatalf("a mensagem devia ter voltado ao outbox: %v", err)
	}
	if status != "pending" || routingKey != key {
		t.Fatalf("outbox = %s %s, want pending %s", status, routingKey, key)
	}
	if left, _ := postgres.NewOperationsReader(pool).ListDeadLetters(ctx, "open", "evt_late"); len(left) != 0 {
		t.Fatalf("a dead letter devia estar fechada: %+v", left)
	}

	// Uma dead letter sem origem no outbox (mensagem estranha) continua recusada.
	err = postgres.NewDeadLetterRepository(pool).Archive(ctx, app.ArchivedMessage{
		MessageID: "estranha", RoutingKey: "x", Payload: []byte(`{}`), Reason: "poison", Deliveries: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	stranger := openDeadLetter(t, "estranha")
	if err := postgres.NewDeadLetterRepository(pool).Requeue(ctx, stranger.Source, stranger.ID, "ana", "x"); !errors.Is(err, app.ErrNoOutboxOrigin) {
		t.Fatalf("Requeue of a message without outbox origin = %v, want ErrNoOutboxOrigin", err)
	}
}

// A trilha não mente depois da limpeza: a etapa não vira "nunca publicada".
func TestTrailAndListStillSayPublishedAfterPurge(t *testing.T) {
	testinfra.ResetDatabase(t, pool)
	ctx := context.Background()
	ingest(t, capture("evt_trail", "txn_trail", 100))
	publishAndAge(t, "evt_trail")
	if n, err := postgres.NewRetention(pool).PurgePublishedOutbox(ctx, time.Now().AddDate(0, 0, -7), 100); err != nil || n != 1 {
		t.Fatalf("purged = %d, %v", n, err)
	}
	reader := postgres.NewOperationsReader(pool)
	trail, err := reader.EventTrail(ctx, "evt_trail")
	if err != nil || trail.Outbox == nil || !trail.Outbox.Archived || trail.Outbox.Status != "published" || trail.Outbox.PublishedAt == nil {
		t.Fatalf("trail outbox = %+v, %v", trail.Outbox, err)
	}
	page, err := reader.ListEvents(ctx, app.EventFilter{Search: "txn_trail"})
	if err != nil || len(page.Items) != 1 || page.Items[0].OutboxStatus != "published" {
		t.Fatalf("list = %+v, %v", page, err)
	}
}

// Se a cópia do arquivo sumir, a trilha falha em vez de mostrar o resumo como payload.
func TestTrailFailsWhenTheArchiveCopyIsMissing(t *testing.T) {
	testinfra.ResetDatabase(t, pool)
	ctx := context.Background()
	ingest(t, capture("evt_gone", "txn_gone", 100))
	ageEvent(t, "evt_gone", time.Now().AddDate(0, -3, 0).UTC())
	if n, err := postgres.NewRetention(pool).ArchiveEventPayloads(ctx, time.Now().AddDate(0, 0, -30), 100); err != nil || n != 1 {
		t.Fatalf("archived = %d, %v", n, err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM processed_events_archive"); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.NewOperationsReader(pool).EventTrail(ctx, "evt_gone"); err == nil {
		t.Fatal("trilha de evento arquivado sem cópia deve falhar")
	}
}
