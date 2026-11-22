//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/postgres"
	"github.com/JohnnyMatteus/payments-events-go/internal/app"
	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
	"github.com/JohnnyMatteus/payments-events-go/internal/testinfra"
)

func reconcileDay(t *testing.T, store *postgres.ReconciliationStore, stmt []domain.StatementLine) app.RunSummary {
	t.Helper()
	ctx := context.Background()
	day := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	period := domain.Period{From: day, To: day.AddDate(0, 0, 1)}
	entries, err := postgres.NewLedgerReader(pool).EntriesBetween(ctx, period.From, period.To)
	if err != nil {
		t.Fatal(err)
	}
	report := domain.Reconcile(period, stmt, entries)
	sum, err := store.SaveRun(ctx, app.RunRecord{
		Date: day, Statement: app.StatementObject{Ref: "s3://b/settlements/2026-11-20.csv"}, Trigger: app.TriggerManual,
		TriggeredBy: "ana", StartedAt: baseTime, FinishedAt: baseTime,
	}, &report)
	if err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	return sum
}

// O ajuste proposto por uma pessoa e aprovado por outra grava ledger e
// outbox, e a conciliação seguinte fecha o par e resolve o caso.
func TestAdjustmentFlowClosesDivergence(t *testing.T) {
	testinfra.ResetDatabase(t, pool)
	ctx := context.Background()
	ingest(t, capture("evt_1", "txn_1", 10000))

	store := postgres.NewReconciliationStore(pool)
	cases := postgres.NewCaseRepository(pool)
	svc := app.NewCaseService(cases)
	stmt := []domain.StatementLine{{LineNumber: 2, TransactionID: "txn_1", PaymentID: "pay_txn_1", Direction: domain.Credit,
		AmountCents: 9500, Currency: "BRL", OccurredAt: baseTime}}

	first := reconcileDay(t, store, stmt)
	if first.Counts[string(domain.AmountMismatch)] != 1 {
		t.Fatalf("counts = %v", first.Counts)
	}
	list, err := cases.ListCases(ctx, app.CaseFilter{Status: "open"})
	if err != nil || len(list) != 1 {
		t.Fatalf("open cases = %v, %v", list, err)
	}
	items, err := store.RunItems(ctx, first.ID)
	if err != nil || len(items) != 1 || items[0].CaseID == nil || items[0].DeltaCents != -500 {
		t.Fatalf("items = %+v, %v", items, err)
	}

	adj, err := svc.ProposeAdjustment(as(ctx, "ana"), list[0].ID, "provedor reteve taxa")
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if _, err := svc.ProposeAdjustment(as(ctx, "ana"), list[0].ID, "de novo"); err == nil {
		t.Fatal("a second proposal on the same case must be refused")
	}
	if _, err := svc.ApproveAdjustment(as(ctx, "ana"), adj.ID, ""); !errors.Is(err, domain.ErrSameOperator) {
		t.Fatalf("self approval = %v", err)
	}
	if count(t, "ledger_entries") != 1 {
		t.Fatal("ledger must be untouched before approval")
	}
	if _, err := svc.ApproveAdjustment(as(ctx, "bruno"), adj.ID, "ok"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if count(t, "ledger_entries") != 2 || count(t, "outbox_messages") != 2 {
		t.Fatalf("ledger=%d outbox=%d, want 2 each", count(t, "ledger_entries"), count(t, "outbox_messages"))
	}

	second := reconcileDay(t, store, stmt)
	if second.Matched != 1 || len(second.Counts) != 0 {
		t.Fatalf("after adjustment: matched=%d counts=%v", second.Matched, second.Counts)
	}
	got, err := cases.GetCase(ctx, list[0].ID)
	if err != nil || got.Status != domain.CaseResolved || got.ResolvedBy != "bruno" {
		t.Fatalf("case = %+v, %v", got, err)
	}
}

// Mensagem morta do broker volta ao outbox com o mesmo message_id.
func TestDeadLetterRequeueRearmsOutbox(t *testing.T) {
	testinfra.ResetDatabase(t, pool)
	ctx := context.Background()
	ingest(t, capture("evt_dl", "txn_dl", 100))
	if _, err := pool.Exec(ctx, `UPDATE outbox_messages SET status = 'published', published_at = now()`); err != nil {
		t.Fatal(err)
	}
	repo := postgres.NewDeadLetterRepository(pool)
	for range 2 {
		if err := repo.Archive(ctx, app.ArchivedMessage{MessageID: "evt_dl", RoutingKey: "ledger.entry_recorded",
			Payload: []byte("{}"), Reason: "rejected", Error: "x", Deliveries: 1}); err != nil {
			t.Fatal(err)
		}
	}
	reader := postgres.NewOperationsReader(pool)
	open, err := reader.ListDeadLetters(ctx, "open", "")
	if err != nil || len(open) != 1 || open[0].Deliveries != 2 || !open[0].HasOutbox {
		t.Fatalf("open = %+v, %v", open, err)
	}
	if err := repo.Requeue(ctx, app.SourceBroker, open[0].ID, "ana", "banco voltou"); err != nil {
		t.Fatalf("Requeue: %v", err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM outbox_messages WHERE message_id = 'evt_dl'`).Scan(&status); err != nil || status != "pending" {
		t.Fatalf("outbox status = %q, %v", status, err)
	}
	if err := repo.Requeue(ctx, app.SourceBroker, open[0].ID, "ana", "again"); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("second requeue = %v, want ErrNotFound", err)
	}
}

func TestEventListAndTrail(t *testing.T) {
	testinfra.ResetDatabase(t, pool)
	ctx := context.Background()
	ev := capture("evt_a", "txn_a", 4200)
	ev.MerchantID = "loja_1"
	ingest(t, ev)
	ingest(t, ev)
	reader := postgres.NewOperationsReader(pool)

	page, err := reader.ListEvents(ctx, app.EventFilter{Search: "TXN_A"})
	if err != nil || len(page.Items) != 1 || page.Items[0].Deliveries != 2 || page.Items[0].MerchantID != "loja_1" {
		t.Fatalf("page = %+v, %v", page, err)
	}
	trail, err := reader.EventTrail(ctx, "evt_a")
	if err != nil || trail.Ledger == nil || trail.Outbox == nil || trail.Outbox.Status != "pending" {
		t.Fatalf("trail = %+v, %v", trail, err)
	}
	if _, err := reader.EventTrail(ctx, "nope"); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("missing event = %v", err)
	}
	balances, err := reader.MerchantBalances(ctx)
	if err != nil || len(balances) != 1 || balances[0].LedgerNetCents != 4200 || balances[0].UnprojectedMessages != 1 {
		t.Fatalf("balances = %+v, %v", balances, err)
	}
	if _, err := reader.PipelineHealth(ctx, time.Now()); err != nil {
		t.Fatalf("PipelineHealth: %v", err)
	}
}

func TestObjectClaimsAreAtomicAndReleasable(t *testing.T) {
	testinfra.ResetDatabase(t, pool)
	ctx := context.Background()
	claims := postgres.NewObjectClaims(pool)
	first, err := claims.Claim(ctx, "b", "settlements/2026-11-20.csv", "v1")
	if err != nil || !first {
		t.Fatalf("first claim = %v, %v", first, err)
	}
	if again, err := claims.Claim(ctx, "b", "settlements/2026-11-20.csv", "v1"); err != nil || again {
		t.Fatalf("second claim = %v, %v, want false", again, err)
	}
	if other, err := claims.Claim(ctx, "b", "settlements/2026-11-20.csv", "v2"); err != nil || !other {
		t.Fatalf("new version claim = %v, %v", other, err)
	}
	if err := claims.Release(ctx, "b", "settlements/2026-11-20.csv", "v1"); err != nil {
		t.Fatal(err)
	}
	if again, err := claims.Claim(ctx, "b", "settlements/2026-11-20.csv", "v1"); err != nil || !again {
		t.Fatalf("claim after release = %v, %v", again, err)
	}
}

// as devolve um contexto com um operador autenticado, como o middleware faz.
func as(ctx context.Context, name string) context.Context {
	return app.WithActor(ctx, app.Actor{ID: "dev:" + name, Name: name, Role: app.RoleOperator})
}

func TestPaymentBalanceReadsProjectionAndLedger(t *testing.T) {
	testinfra.ResetDatabase(t, pool)
	ctx := context.Background()
	ev := capture("evt_bal", "txn_bal", 15000)
	ev.MerchantID = "loja_1"
	ingest(t, ev)
	reader := postgres.NewOperationsReader(pool)
	if _, err := reader.PaymentBalance(ctx, "pay_txn_bal"); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("antes da projeção = %v, want ErrNotFound", err)
	}
	_, err := postgres.NewProjectionRepository(pool).ApplyOnce(ctx, "balance-projector", "evt_bal", app.LedgerEntryRecorded{
		SchemaVersion: 1, EventID: "evt_bal", TransactionID: "txn_bal", PaymentID: "pay_txn_bal",
		Direction: domain.Credit, AmountCents: 15000, Currency: "BRL", OccurredAt: baseTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := reader.PaymentBalance(ctx, "pay_txn_bal")
	if err != nil || got.MerchantID != "loja_1" || got.CapturedCents != 15000 || got.LedgerNetCents != 15000 || got.Currency != "BRL" {
		t.Fatalf("PaymentBalance = %+v, %v", got, err)
	}
}
