package domain_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
)

var day = domain.Period{
	From: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC),
	To:   time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC),
}

func at(h int) time.Time { return day.From.Add(time.Duration(h) * time.Hour) }

func line(n int, tx string, dir domain.Direction, cents int64, ts time.Time) domain.StatementLine {
	return domain.StatementLine{
		LineNumber: n, TransactionID: tx, PaymentID: "pay_" + tx,
		Direction: dir, AmountCents: cents, Currency: "BRL", OccurredAt: ts,
	}
}

func entry(tx string, dir domain.Direction, cents int64, ts time.Time) domain.LedgerEntry {
	return domain.LedgerEntry{
		EventID: "evt_" + tx, TransactionID: tx, PaymentID: "pay_" + tx,
		Direction: dir, AmountCents: cents, Currency: "BRL", OccurredAt: ts,
	}
}

func TestReconcileAllMatched(t *testing.T) {
	t.Parallel()
	statement := []domain.StatementLine{
		line(2, "txn_a", domain.Credit, 10000, at(9)),
		line(3, "txn_b", domain.Debit, 2500, at(10)),
	}
	ledger := []domain.LedgerEntry{
		entry("txn_a", domain.Credit, 10000, at(9)),
		entry("txn_b", domain.Debit, 2500, at(10)),
	}
	r := domain.Reconcile(day, statement, ledger)
	if r.HasDivergences() {
		t.Fatalf("unexpected divergences: %+v", r.Divergences)
	}
	if r.Matched != 2 || r.StatementLines != 2 || r.LedgerEntries != 2 {
		t.Fatalf("counts = %+v", r)
	}
	if r.StatementNetCents != 7500 || r.LedgerNetCents != 7500 {
		t.Fatalf("net: statement=%d ledger=%d, want 7500", r.StatementNetCents, r.LedgerNetCents)
	}
}

func TestReconcileDivergences(t *testing.T) {
	t.Parallel()
	statement := []domain.StatementLine{
		line(2, "txn_ok", domain.Credit, 10000, at(1)),
		line(3, "txn_missing", domain.Credit, 5000, at(2)),   // faltando no ledger
		line(4, "txn_amount", domain.Credit, 9990, at(3)),    // valor divergente
		line(5, "txn_direction", domain.Debit, 700, at(4)),   // direção divergente
		line(6, "txn_ok", domain.Credit, 10000, at(5)),       // duplicada no extrato
		line(7, "txn_yesterday", domain.Credit, 100, at(-1)), // fora do período
	}
	ledger := []domain.LedgerEntry{
		entry("txn_ok", domain.Credit, 10000, at(1)),
		entry("txn_amount", domain.Credit, 10000, at(3)),
		entry("txn_direction", domain.Credit, 700, at(4)),
		entry("txn_extra", domain.Credit, 300, at(6)),     // sobrando no ledger
		entry("txn_tomorrow", domain.Credit, 100, at(24)), // fora do período
	}

	r := domain.Reconcile(day, statement, ledger)

	got := make([]string, 0, len(r.Divergences))
	for _, d := range r.Divergences {
		got = append(got, fmt.Sprintf("%s:%s", d.Kind, d.TransactionID))
	}
	want := []string{
		"amount_mismatch:txn_amount",
		"amount_mismatch:txn_direction",
		"duplicate_in_statement:txn_ok",
		"missing_in_ledger:txn_missing",
		"missing_in_statement:txn_extra",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("divergences\n got: %v\nwant: %v", got, want)
	}
	if r.Matched != 1 || r.SkippedOutOfPeriod != 1 || r.LedgerEntries != 4 || r.StatementLines != 5 {
		t.Fatalf("counts = matched %d skipped %d ledger %d statement %d",
			r.Matched, r.SkippedOutOfPeriod, r.LedgerEntries, r.StatementLines)
	}
	if c := r.CountByKind(); c[domain.AmountMismatch] != 2 || c[domain.MissingInLedger] != 1 {
		t.Fatalf("CountByKind = %v", c)
	}
	if !strings.Contains(r.Divergences[0].Detail, "valor: extrato 99.90, ledger 100.00") {
		t.Fatalf("detail sem os valores: %q", r.Divergences[0].Detail)
	}
	if !strings.Contains(r.Divergences[1].Detail, "direção: extrato debit, ledger credit") {
		t.Fatalf("detail sem a direção: %q", r.Divergences[1].Detail)
	}
}

func TestReconcileCurrencyMismatch(t *testing.T) {
	t.Parallel()
	l := line(2, "txn_fx", domain.Credit, 1000, at(1))
	l.Currency = "USD"
	r := domain.Reconcile(day, []domain.StatementLine{l}, []domain.LedgerEntry{entry("txn_fx", domain.Credit, 1000, at(1))})
	if len(r.Divergences) != 1 || r.Divergences[0].Kind != domain.AmountMismatch {
		t.Fatalf("divergences = %+v", r.Divergences)
	}
	if !strings.Contains(r.Divergences[0].Detail, "moeda: extrato USD, ledger BRL") {
		t.Fatalf("detail = %q", r.Divergences[0].Detail)
	}
}

func TestReconcilePeriodBoundaries(t *testing.T) {
	t.Parallel()
	statement := []domain.StatementLine{
		line(2, "txn_start", domain.Credit, 100, day.From), // incluído: início fechado
		line(3, "txn_end", domain.Credit, 100, day.To),     // excluído: fim aberto
		line(4, "txn_last", domain.Credit, 100, day.To.Add(-time.Nanosecond)),
	}
	ledger := []domain.LedgerEntry{
		entry("txn_start", domain.Credit, 100, day.From),
		entry("txn_last", domain.Credit, 100, day.To.Add(-time.Nanosecond)),
	}
	r := domain.Reconcile(day, statement, ledger)
	if r.HasDivergences() || r.Matched != 2 || r.SkippedOutOfPeriod != 1 {
		t.Fatalf("report = %+v", r)
	}
}

func TestReconcileEmptyInputs(t *testing.T) {
	t.Parallel()
	r := domain.Reconcile(day, nil, nil)
	if r.HasDivergences() || r.Matched != 0 {
		t.Fatalf("report = %+v", r)
	}
}

func BenchmarkReconcile(b *testing.B) {
	const n = 10_000
	statement := make([]domain.StatementLine, n)
	ledger := make([]domain.LedgerEntry, n)
	for i := range n {
		tx := fmt.Sprintf("txn_%06d", i)
		ts := day.From.Add(time.Duration(i) * time.Second)
		statement[i] = line(i+2, tx, domain.Credit, int64(i+1), ts)
		ledger[i] = entry(tx, domain.Credit, int64(i+1), ts)
	}
	b.ReportAllocs()
	for b.Loop() {
		domain.Reconcile(day, statement, ledger)
	}
}

// O lançamento do provedor dá a identidade da transação mesmo quando o ajuste
// vem antes dele no ledger (ordem de leitura não pode mudar o resultado).
func TestReconcileAdjustmentBeforeProviderEntryKeepsProviderIdentity(t *testing.T) {
	t.Parallel()
	provider := entry("txn_a", domain.Credit, 3000, at(9))
	adj := domain.LedgerEntry{
		EventID: "adj_1", TransactionID: "adj_c1", AdjustsTransactionID: "txn_a", PaymentID: "pay_txn_a",
		Direction: domain.Credit, AmountCents: 500, Currency: "BRL", OccurredAt: at(11),
	}
	statement := []domain.StatementLine{line(2, "txn_a", domain.Credit, 3500, at(9))}
	for name, ledger := range map[string][]domain.LedgerEntry{
		"ajuste antes":  {adj, provider},
		"ajuste depois": {provider, adj},
	} {
		r := domain.Reconcile(day, statement, ledger)
		if r.HasDivergences() || r.Matched != 1 || !r.Matches[0].Adjusted {
			t.Fatalf("%s: report = %+v", name, r)
		}
		if got := r.Matches[0].Ledger; got.EventID != "evt_txn_a" || !got.OccurredAt.Equal(at(9)) || got.AmountCents != 3500 {
			t.Errorf("%s: o lançamento efetivo deve ter a identidade do provedor e o líquido 3500: %+v", name, got)
		}
	}
}

// Ajuste que zera a transação: o efetivo tem valor 0 e, se o extrato ainda a
// lista, a divergência mostra o lado do ledger como crédito de 0, não débito.
func TestReconcileZeroNetEntryKeepsCreditDirection(t *testing.T) {
	t.Parallel()
	ledger := []domain.LedgerEntry{
		entry("txn_z", domain.Credit, 1000, at(9)),
		{EventID: "adj_z", TransactionID: "adj_cz", AdjustsTransactionID: "txn_z", PaymentID: "pay_txn_z",
			Direction: domain.Debit, AmountCents: 1000, Currency: "BRL", OccurredAt: at(10)},
	}
	r := domain.Reconcile(day, []domain.StatementLine{line(2, "txn_z", domain.Credit, 1000, at(9))}, ledger)
	if len(r.Divergences) != 1 || r.Divergences[0].Kind != domain.AmountMismatch {
		t.Fatalf("divergences = %+v", r.Divergences)
	}
	if got := r.Divergences[0].Ledger; got.AmountCents != 0 || got.Direction != domain.Credit {
		t.Fatalf("ledger efetivo = %+v, want crédito de 0", got)
	}
}
