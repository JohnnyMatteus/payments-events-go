//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/postgres"
	"github.com/JohnnyMatteus/payments-events-go/internal/app"
	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
	"github.com/JohnnyMatteus/payments-events-go/internal/testinfra"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	os.Exit(testinfra.Run(m, func(ctx context.Context) (func(), error) {
		p, teardown, err := testinfra.StartPostgres(ctx)
		pool = p
		return teardown, err
	}))
}

var baseTime = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

func capture(id, tx string, cents int64) domain.Event {
	return domain.Event{
		ID: id, Type: domain.EventPaymentCaptured, TransactionID: tx, PaymentID: "pay_" + tx,
		AmountCents: cents, Currency: "BRL", OccurredAt: baseTime,
	}
}

func ingest(t *testing.T, ev domain.Event) app.IngestOutcome {
	t.Helper()
	svc := app.NewIngestService(postgres.NewEventRecorder(pool))
	outcome, err := svc.Ingest(context.Background(), ev, []byte(`{"id":"`+ev.ID+`"}`))
	if err != nil {
		t.Fatalf("Ingest(%s): %v", ev.ID, err)
	}
	return outcome
}

func count(t *testing.T, table string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestMigrateIsIdempotent(t *testing.T) {
	applied, err := postgres.Migrate(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 0 {
		t.Fatalf("second run applied %v, want nothing", applied)
	}
}

func TestRecordWritesLedgerAndOutboxAtomically(t *testing.T) {
	testinfra.ResetDatabase(t, pool)

	if got := ingest(t, capture("evt_1", "txn_1", 15000)); got != app.OutcomeRecorded {
		t.Fatalf("outcome = %s", got)
	}
	if count(t, "processed_events") != 1 || count(t, "ledger_entries") != 1 || count(t, "outbox_messages") != 1 {
		t.Fatal("expected one row in each table")
	}
	var status, routingKey string
	if err := pool.QueryRow(context.Background(),
		"SELECT status, routing_key FROM outbox_messages WHERE message_id = 'evt_1'").Scan(&status, &routingKey); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || routingKey != app.RoutingKeyLedgerEntryRecorded {
		t.Fatalf("outbox row: status=%s routing_key=%s", status, routingKey)
	}
}

func TestRecordDuplicateDeliveryDoesNotReprocess(t *testing.T) {
	testinfra.ResetDatabase(t, pool)

	ingest(t, capture("evt_1", "txn_1", 15000))
	if got := ingest(t, capture("evt_1", "txn_1", 15000)); got != app.OutcomeDuplicate {
		t.Fatalf("outcome = %s, want duplicate", got)
	}
	if count(t, "ledger_entries") != 1 || count(t, "outbox_messages") != 1 {
		t.Fatal("duplicate delivery must not write again")
	}
}

func TestRecordConcurrentDuplicateDeliveries(t *testing.T) {
	testinfra.ResetDatabase(t, pool)

	const deliveries = 20
	outcomes := make(chan app.IngestOutcome, deliveries)
	var wg sync.WaitGroup
	for range deliveries {
		wg.Go(func() { outcomes <- ingest(t, capture("evt_race", "txn_race", 999)) })
	}
	wg.Wait()
	close(outcomes)

	got := map[app.IngestOutcome]int{}
	for o := range outcomes {
		got[o]++
	}
	if got[app.OutcomeRecorded] != 1 || got[app.OutcomeDuplicate] != deliveries-1 {
		t.Fatalf("outcomes = %v, want exactly one recorded", got)
	}
	if count(t, "ledger_entries") != 1 {
		t.Fatal("concurrent deliveries must produce a single ledger entry")
	}
}

func TestRecordSecondEventForSameTransaction(t *testing.T) {
	testinfra.ResetDatabase(t, pool)

	ingest(t, capture("evt_a", "txn_same", 100))
	if got := ingest(t, capture("evt_b", "txn_same", 100)); got != app.OutcomeDuplicateTransaction {
		t.Fatalf("outcome = %s, want duplicate_transaction", got)
	}
	if count(t, "ledger_entries") != 1 || count(t, "outbox_messages") != 1 || count(t, "processed_events") != 2 {
		t.Fatal("second event for the same transaction must be audited but not posted")
	}
}

func TestRecordIgnoredEventIsIdempotent(t *testing.T) {
	testinfra.ResetDatabase(t, pool)

	ev := domain.Event{ID: "evt_dispute", Type: "payment.dispute_opened"}
	if got := ingest(t, ev); got != app.OutcomeIgnored {
		t.Fatalf("outcome = %s", got)
	}
	if got := ingest(t, ev); got != app.OutcomeDuplicate {
		t.Fatalf("outcome = %s", got)
	}
	if count(t, "ledger_entries") != 0 || count(t, "outbox_messages") != 0 {
		t.Fatal("ignored event must not post")
	}
}

func TestOutboxProcessDueAppliesDecisions(t *testing.T) {
	testinfra.ResetDatabase(t, pool)
	ingest(t, capture("evt_ok", "txn_ok", 1))
	ingest(t, capture("evt_retry", "txn_retry", 2))
	ingest(t, capture("evt_dead", "txn_dead", 3))

	repo := postgres.NewOutboxRepository(pool)
	now := time.Now().Add(time.Second)
	retryAt := now.Add(time.Minute)
	n, err := repo.ProcessDue(context.Background(), now, 10, func(_ context.Context, m app.OutboxMessage) app.Delivery {
		switch m.MessageID {
		case "evt_ok":
			if m.Headers == nil {
				t.Error("headers must be decoded")
			}
			return app.Delivery{Status: app.DeliveryPublished}
		case "evt_retry":
			return app.Delivery{Status: app.DeliveryRetry, NextAttemptAt: retryAt, Err: errors.New("nack")}
		default:
			return app.Delivery{Status: app.DeliveryDead, Err: errors.New("gave up")}
		}
	})
	if err != nil || n != 3 {
		t.Fatalf("ProcessDue = %d, %v", n, err)
	}

	rows, err := pool.Query(context.Background(),
		"SELECT message_id, status, attempts, coalesce(last_error, '') FROM outbox_messages ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := map[string]string{"evt_ok": "published/0/", "evt_retry": "pending/1/nack", "evt_dead": "dead/1/gave up"}
	for rows.Next() {
		var id, status, lastErr string
		var attempts int
		if err := rows.Scan(&id, &status, &attempts, &lastErr); err != nil {
			t.Fatal(err)
		}
		if got := status + "/" + strconv.Itoa(attempts) + "/" + lastErr; got != want[id] {
			t.Errorf("%s = %s, want %s", id, got, want[id])
		}
	}

	// A mensagem reagendada não está vencida agora; publicadas e mortas nunca voltam.
	n, err = repo.ProcessDue(context.Background(), now, 10, func(context.Context, app.OutboxMessage) app.Delivery {
		t.Error("no message should be due")
		return app.Delivery{Status: app.DeliveryPublished}
	})
	if err != nil || n != 0 {
		t.Fatalf("second ProcessDue = %d, %v", n, err)
	}
}

func TestOutboxConcurrentRelaysDoNotShareMessages(t *testing.T) {
	testinfra.ResetDatabase(t, pool)
	for i := range 50 {
		ingest(t, capture("evt_"+strconv.Itoa(i), "txn_"+strconv.Itoa(i), int64(i+1)))
	}

	repo := postgres.NewOutboxRepository(pool)
	var (
		mu   sync.Mutex
		seen = map[string]int{}
		wg   sync.WaitGroup
	)
	for range 5 {
		wg.Go(func() {
			for {
				n, err := repo.ProcessDue(context.Background(), time.Now().Add(time.Second), 7,
					func(_ context.Context, m app.OutboxMessage) app.Delivery {
						mu.Lock()
						seen[m.MessageID]++
						mu.Unlock()
						time.Sleep(time.Millisecond)
						return app.Delivery{Status: app.DeliveryPublished}
					})
				if err != nil {
					t.Error(err)
					return
				}
				if n == 0 {
					return
				}
			}
		})
	}
	wg.Wait()

	if len(seen) != 50 {
		t.Fatalf("delivered %d distinct messages, want 50", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("message %s delivered %d times", id, n)
		}
	}
}

func TestLedgerReaderFiltersByPeriod(t *testing.T) {
	testinfra.ResetDatabase(t, pool)
	inside := capture("evt_in", "txn_in", 10)
	before := capture("evt_before", "txn_before", 20)
	before.OccurredAt = baseTime.Add(-24 * time.Hour)
	ingest(t, inside)
	ingest(t, before)

	from := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	entries, err := postgres.NewLedgerReader(pool).EntriesBetween(context.Background(), from, from.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].TransactionID != "txn_in" || entries[0].Direction != domain.Credit ||
		entries[0].Currency != "BRL" || !entries[0].OccurredAt.Equal(baseTime) {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestProjectionAppliesEachMessageOnce(t *testing.T) {
	testinfra.ResetDatabase(t, pool)
	repo := postgres.NewProjectionRepository(pool)
	ctx := context.Background()
	capture := app.LedgerEntryRecorded{SchemaVersion: 1, PaymentID: "pay_1", TransactionID: "txn_1",
		Direction: domain.Credit, AmountCents: 10000, Currency: "BRL"}
	refund := capture
	refund.TransactionID, refund.Direction, refund.AmountCents = "txn_2", domain.Debit, 2500

	for _, step := range []struct {
		id   string
		msg  app.LedgerEntryRecorded
		want bool
	}{
		{"evt_1", capture, true},
		{"evt_1", capture, false},
		{"evt_2", refund, true},
	} {
		applied, err := repo.ApplyOnce(ctx, "balance-projector", step.id, step.msg)
		if err != nil || applied != step.want {
			t.Fatalf("ApplyOnce(%s) = %v, %v; want %v", step.id, applied, err, step.want)
		}
	}
	var capturedCents, refundedCents int64
	if err := pool.QueryRow(ctx, "SELECT captured_cents, refunded_cents FROM payment_balances WHERE payment_id = 'pay_1'").
		Scan(&capturedCents, &refundedCents); err != nil {
		t.Fatal(err)
	}
	if capturedCents != 10000 || refundedCents != 2500 {
		t.Fatalf("balance = captured %d refunded %d", capturedCents, refundedCents)
	}
}
