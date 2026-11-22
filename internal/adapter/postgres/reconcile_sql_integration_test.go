//go:build integration

package postgres_test

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"time"

	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/postgres"
	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
	"github.com/JohnnyMatteus/payments-events-go/internal/testinfra"
)

var sqlDay = domain.Period{
	From: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC),
	To:   time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC),
}

type ledgerRow struct {
	eventID, tx, adjusts, currency string
	dir                            domain.Direction
	cents                          int64
	at                             time.Time
}

// insertLedger grava direto (o ledger aceita INSERT; só UPDATE, DELETE e TRUNCATE são recusados).
func insertLedger(t *testing.T, r ledgerRow) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO processed_events (event_id, event_type, outcome, payload)
		VALUES ($1, 'payment.captured', 'recorded', '{}')`, r.eventID); err != nil {
		t.Fatal(err)
	}
	kind := "provider"
	if r.adjusts != "" {
		kind = "adjustment"
	}
	if r.currency == "" {
		r.currency = "BRL"
	}
	if _, err := pool.Exec(ctx, `INSERT INTO ledger_entries (event_id, merchant_id, transaction_id, payment_id, direction,
		amount_cents, currency, occurred_at, kind, adjusts_transaction_id)
		VALUES ($1, 'loja', $2, $3, $4, $5, $6, $7, $8, NULLIF($9, ''))`,
		r.eventID, r.tx, "pay_"+r.tx, string(r.dir), r.cents, r.currency, r.at, kind, r.adjusts); err != nil {
		t.Fatal(err)
	}
}

func stmtLine(n int, tx string, dir domain.Direction, cents int64, cur domain.Currency, at time.Time) domain.StatementLine {
	return domain.StatementLine{LineNumber: n, TransactionID: tx, PaymentID: "pay_" + tx, Direction: dir,
		AmountCents: cents, Currency: cur, OccurredAt: at}
}

// compareBoth roda a comparação em memória e a do banco sobre os mesmos dados.
func compareBoth(t *testing.T, statement []domain.StatementLine, includeMatches bool) (memory, sql domain.Report) {
	t.Helper()
	ctx := context.Background()
	entries, err := postgres.NewLedgerReader(pool).EntriesBetween(ctx, sqlDay.From, sqlDay.To)
	if err != nil {
		t.Fatal(err)
	}
	memory = domain.Reconcile(sqlDay, statement, entries)
	sql, err = postgres.NewSQLComparer(pool, includeMatches).Reconcile(ctx, sqlDay, statement)
	if err != nil {
		t.Fatalf("SQLComparer: %v", err)
	}
	return memory, sql
}

// Os casos de cada ramo da regra: par conciliado, valor e moeda diferentes,
// faltando de cada lado, duplicata no extrato, fora do período, ajuste somado,
// ajuste sem lançamento do provedor e transação estornada por ajuste.
func TestSQLComparerMatchesTheInMemoryReconciliation(t *testing.T) {
	testinfra.ResetDatabase(t, pool)
	at := func(h int) time.Time { return sqlDay.From.Add(time.Duration(h) * time.Hour) }
	for _, r := range []ledgerRow{
		{eventID: "e1", tx: "txn_ok", dir: domain.Credit, cents: 10000, at: at(9)},
		{eventID: "e2", tx: "txn_amount", dir: domain.Credit, cents: 2500, at: at(10)},
		{eventID: "e3", tx: "txn_extra", dir: domain.Credit, cents: 8000, at: at(11)},
		{eventID: "e4", tx: "txn_adj", dir: domain.Credit, cents: 3000, at: at(12)},
		{eventID: "e5", tx: "adj_c1", adjusts: "txn_adj", dir: domain.Credit, cents: 500, at: at(13)},
		{eventID: "e6", tx: "txn_rev", dir: domain.Credit, cents: 1000, at: at(14)},
		{eventID: "e7", tx: "adj_c2", adjusts: "txn_rev", dir: domain.Debit, cents: 1000, at: at(15)},
		{eventID: "e8", tx: "txn_out", dir: domain.Credit, cents: 700, at: sqlDay.From.Add(-time.Hour)},
		{eventID: "e9", tx: "adj_c3", adjusts: "txn_ghost", dir: domain.Credit, cents: 400, at: at(16)},
		{eventID: "e10", tx: "txn_cur", dir: domain.Credit, cents: 100, at: at(17)},
		{eventID: "e11", tx: "txn_debit", dir: domain.Debit, cents: 900, at: at(18)},
	} {
		insertLedger(t, r)
	}
	statement := []domain.StatementLine{
		stmtLine(2, "txn_ok", domain.Credit, 10000, "BRL", at(9)),
		stmtLine(3, "txn_amount", domain.Credit, 3000, "BRL", at(10)),
		stmtLine(4, "txn_adj", domain.Credit, 3500, "BRL", at(12)),
		stmtLine(5, "txn_ok", domain.Credit, 10000, "BRL", at(9)), // duplicata
		stmtLine(6, "txn_missing", domain.Credit, 5000, "BRL", at(19)),
		stmtLine(7, "txn_out", domain.Credit, 700, "BRL", sqlDay.From.Add(-time.Hour)), // fora do período
		stmtLine(8, "txn_ghost", domain.Credit, 400, "BRL", at(16)),
		stmtLine(9, "txn_cur", domain.Credit, 100, "USD", at(17)),
		stmtLine(10, "txn_debit", domain.Credit, 900, "BRL", at(18)), // direção diferente
	}
	memory, sql := compareBoth(t, statement, true)
	if !reflect.DeepEqual(memory, sql) {
		t.Fatalf("a comparação no banco difere da em memória:\nmemória: %+v\nbanco:   %+v", memory, sql)
	}
	if memory.Matched == 0 || len(memory.Divergences) < 5 || memory.Reversed != 1 || memory.SkippedOutOfPeriod != 1 {
		t.Fatalf("o cenário não exercita os ramos esperados: %+v", memory)
	}

	// Sem os pares no relatório (volume grande): mesma contagem, sem o slice.
	_, noMatches := compareBoth(t, statement, false)
	want := memory
	want.Matches = nil
	if !reflect.DeepEqual(want, noMatches) {
		t.Fatalf("sem Matches o resto do relatório deve ser igual:\n%+v\n%+v", want, noMatches)
	}
}

// Dados aleatórios (semente fixa): a comparação no banco não pode divergir da regra em memória.
func TestSQLComparerMatchesTheInMemoryReconciliationOnRandomData(t *testing.T) {
	testinfra.ResetDatabase(t, pool)
	rng := rand.New(rand.NewSource(20261122))
	var statement []domain.StatementLine
	line := 2
	for i := 0; i < 250; i++ {
		tx := fmt.Sprintf("txn_r%03d", i)
		at := sqlDay.From.Add(time.Duration(rng.Intn(26*60)-60) * time.Minute) // alguns fora do período
		cents := int64(100 + rng.Intn(9000))
		dir := domain.Credit
		if rng.Intn(5) == 0 {
			dir = domain.Debit
		}
		if rng.Intn(10) != 0 { // 90% têm lançamento do provedor
			insertLedger(t, ledgerRow{eventID: "ev_" + tx, tx: tx, dir: dir, cents: cents, at: at})
			for a := 0; a < rng.Intn(3); a++ { // até dois ajustes
				adjDir := domain.Credit
				if rng.Intn(2) == 0 {
					adjDir = domain.Debit
				}
				insertLedger(t, ledgerRow{eventID: fmt.Sprintf("ev_adj_%s_%d", tx, a), tx: fmt.Sprintf("%s:adj:%d", tx, a),
					adjusts: tx, dir: adjDir, cents: int64(1 + rng.Intn(500)), at: at.Add(time.Duration(a+1) * time.Minute)})
			}
		}
		if rng.Intn(8) != 0 { // 87% aparecem no extrato
			stmtCents := cents
			if rng.Intn(4) == 0 {
				stmtCents += int64(rng.Intn(300))
			}
			cur := domain.Currency("BRL")
			if rng.Intn(20) == 0 {
				cur = "USD"
			}
			statement = append(statement, stmtLine(line, tx, dir, stmtCents, cur, at))
			line++
			if rng.Intn(15) == 0 {
				statement = append(statement, stmtLine(line, tx, dir, stmtCents, cur, at)) // duplicata
				line++
			}
		}
	}
	memory, sql := compareBoth(t, statement, true)
	if !reflect.DeepEqual(memory, sql) {
		t.Fatalf("a comparação no banco difere da em memória (matched %d/%d, divergências %d/%d)",
			memory.Matched, sql.Matched, len(memory.Divergences), len(sql.Divergences))
	}
	if memory.Matched < 20 || len(memory.Divergences) < 20 {
		t.Fatalf("o conjunto aleatório devia ter pares e divergências: %+v", memory.CountByKind())
	}
}
