//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/postgres"
	"github.com/JohnnyMatteus/payments-events-go/internal/app"
	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
	"github.com/JohnnyMatteus/payments-events-go/internal/testinfra"
)

// beginWriter imita um escritor do ledger: lock compartilhado primeiro e INSERT
// depois (o id é sorteado aqui), sem confirmar.
func beginWriter(ctx context.Context, t *testing.T, id string) pgx.Tx {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock_shared($1)", int64(postgres.LedgerAppendLockKey)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO processed_events (event_id, event_type, outcome, payload)
		VALUES ($1, 'payment.captured', 'recorded', '{}')`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO ledger_entries (event_id, transaction_id, payment_id, direction,
		amount_cents, currency, occurred_at) VALUES ($1, $2, 'pay', 'credit', 100, 'BRL', now())`, id, "txn_"+id); err != nil {
		t.Fatal(err)
	}
	return tx
}

func notDoneWithin(done <-chan struct{}, d time.Duration) bool {
	select {
	case <-done:
		return false
	case <-time.After(d):
		return true
	}
}

// Duas transações do ledger confirmam fora de ordem (o id menor confirma por
// último). A leitura por cursor espera o escritor em andamento e depois devolve
// os dois, em ordem, sem pular o id menor.
func TestLedgerCursorNeverSkipsAnOutOfOrderCommit(t *testing.T) {
	testinfra.ResetDatabase(t, pool)
	ctx := context.Background()
	slow := beginWriter(ctx, t, "evt_slow") // id 1, confirma por último
	fast := beginWriter(ctx, t, "evt_fast") // id 2
	if err := fast.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	reader := postgres.NewOperationsReader(pool)
	type result struct {
		page app.Page[app.LedgerView]
		err  error
	}
	got := make(chan result, 1)
	done := make(chan struct{})
	go func() {
		page, err := reader.LedgerEntries(ctx, app.LedgerFilter{Limit: 1})
		got <- result{page, err}
		close(done)
	}()
	if !notDoneWithin(done, 400*time.Millisecond) {
		t.Fatal("a leitura devolveu enquanto o escritor do id 1 estava em andamento: o id 2 apareceria antes do 1")
	}
	if err := slow.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	first := <-got
	if first.err != nil || len(first.page.Items) != 1 || first.page.Items[0].TransactionID != "txn_evt_slow" {
		t.Fatalf("primeira página = %+v, %v; want txn_evt_slow (id 1)", first.page, first.err)
	}
	second, err := reader.LedgerEntries(ctx, app.LedgerFilter{Limit: 1, Cursor: first.page.NextCursor})
	if err != nil || len(second.Items) != 1 || second.Items[0].TransactionID != "txn_evt_fast" {
		t.Fatalf("segunda página = %+v, %v; want txn_evt_fast (id 2)", second, err)
	}
}

// O recorder de verdade também segura o lock compartilhado antes de sortear o
// id: com a leitura (lock exclusivo) em andamento, a gravação espera.
func TestRecorderWaitsForTheLedgerBoundLock(t *testing.T) {
	testinfra.ResetDatabase(t, pool)
	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			_, _ = conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", int64(postgres.LedgerAppendLockKey))
			conn.Release()
		}
	}
	defer release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", int64(postgres.LedgerAppendLockKey)); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		ingest(t, capture("evt_waits", "txn_waits", 100))
		close(done)
	}()
	if !notDoneWithin(done, 400*time.Millisecond) {
		t.Fatal("o recorder gravou um lançamento com o lock exclusivo do leitor retido: não pega o lock compartilhado")
	}
	release()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("o recorder não terminou depois de o lock ser liberado")
	}
	if count(t, "ledger_entries") != 1 {
		t.Fatal("o lançamento devia ter sido gravado")
	}
}

// A aprovação de ajuste também grava no ledger: pega o lock compartilhado antes
// de travar a linha do ajuste (mesma ordem do webhook). Com o lock exclusivo do
// leitor retido, a aprovação espera sem ter travado a linha do ajuste.
func TestApproveAdjustmentTakesTheAppendLockBeforeLockingTheAdjustmentRow(t *testing.T) {
	testinfra.ResetDatabase(t, pool)
	ctx := context.Background()
	ingest(t, capture("evt_1", "txn_1", 10000))
	store := postgres.NewReconciliationStore(pool)
	cases := postgres.NewCaseRepository(pool)
	svc := app.NewCaseService(cases)
	stmt := []domain.StatementLine{{LineNumber: 2, TransactionID: "txn_1", PaymentID: "pay_txn_1", Direction: domain.Credit,
		AmountCents: 9500, Currency: "BRL", OccurredAt: baseTime}}
	reconcileDay(t, store, stmt)
	open, err := cases.ListCases(ctx, app.CaseFilter{Status: "open"})
	if err != nil || len(open) != 1 {
		t.Fatalf("open cases = %v, %v", open, err)
	}
	adj, err := svc.ProposeAdjustment(as(ctx, "ana"), open[0].ID, "provedor reteve taxa")
	if err != nil {
		t.Fatal(err)
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			_, _ = conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", int64(postgres.LedgerAppendLockKey))
			conn.Release()
		}
	}
	defer release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", int64(postgres.LedgerAppendLockKey)); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		_, err := svc.ApproveAdjustment(as(ctx, "bruno"), adj.ID, "ok")
		done <- err
		close(finished)
	}()
	if !notDoneWithin(finished, 400*time.Millisecond) {
		t.Fatal("a aprovação gravou no ledger com o lock exclusivo do leitor retido")
	}
	// Bloqueada no lock do ledger, a aprovação ainda não pode ter travado a linha do ajuste.
	probe, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := probe.Exec(ctx, "SELECT 1 FROM ledger_adjustments WHERE id = $1 FOR UPDATE NOWAIT", adj.ID); err != nil {
		t.Fatalf("a linha do ajuste já estava travada: o lock do ledger deve vir antes do FOR UPDATE: %v", err)
	}
	_ = probe.Rollback(ctx)
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("approve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a aprovação não terminou depois de o lock ser liberado")
	}
	if count(t, "ledger_entries") != 2 {
		t.Fatal("o ajuste aprovado devia virar lançamento")
	}
}
