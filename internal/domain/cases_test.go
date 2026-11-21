package domain_test

import (
	"errors"
	"testing"

	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
)

// Cada ajuste aprovado precisa fazer a divergência sumir na conciliação
// seguinte, sem tocar no lançamento original.
func TestAdjustmentClosesDivergenceOnNextRun(t *testing.T) {
	t.Parallel()
	statement := []domain.StatementLine{
		line(2, "txn_missing", domain.Credit, 5000, at(2)),
		line(3, "txn_amount", domain.Debit, 3000, at(3)),
	}
	ledger := []domain.LedgerEntry{
		entry("txn_amount", domain.Debit, 2500, at(3)),
		entry("txn_extra", domain.Credit, 8000, at(4)),
	}
	first := domain.Reconcile(day, statement, ledger)
	if len(first.Divergences) != 3 {
		t.Fatalf("divergences = %+v, want 3", first.Divergences)
	}
	for i, d := range first.Divergences {
		adj, err := domain.AdjustmentFor(d, string(rune('a'+i)))
		if err != nil {
			t.Fatalf("AdjustmentFor(%s): %v", d.Kind, err)
		}
		ledger = append(ledger, adj)
	}

	second := domain.Reconcile(day, statement, ledger)
	if second.HasDivergences() {
		t.Fatalf("divergences after adjustments: %+v", second.Divergences)
	}
	if second.Matched != 2 || second.Reversed != 1 {
		t.Fatalf("matched=%d reversed=%d, want 2 and 1", second.Matched, second.Reversed)
	}
	if second.StatementNetCents != second.LedgerNetCents {
		t.Fatalf("net: statement=%d ledger=%d", second.StatementNetCents, second.LedgerNetCents)
	}
	for _, m := range second.Matches {
		if m.Statement.TransactionID == "txn_amount" && !m.Adjusted {
			t.Fatal("txn_amount should be flagged as adjusted")
		}
	}
}

func TestAdjustmentForMismatchIsDelta(t *testing.T) {
	t.Parallel()
	st := line(2, "txn_x", domain.Credit, 3000, at(1))
	le := entry("txn_x", domain.Credit, 2500, at(1))
	d := domain.Divergence{Kind: domain.AmountMismatch, TransactionID: "txn_x", Statement: &st, Ledger: &le}
	if d.DeltaCents() != 500 {
		t.Fatalf("delta = %d, want 500", d.DeltaCents())
	}
	adj, err := domain.AdjustmentFor(d, "7")
	if err != nil {
		t.Fatal(err)
	}
	if adj.Direction != domain.Credit || adj.AmountCents != 500 || adj.AdjustsTransactionID != "txn_x" ||
		adj.TransactionID != "txn_x:adj:7" {
		t.Fatalf("adjustment = %+v", adj)
	}
}

func TestAdjustmentRefusedForDuplicateOrCurrency(t *testing.T) {
	t.Parallel()
	st := line(2, "txn_x", domain.Credit, 3000, at(1))
	if _, err := domain.AdjustmentFor(domain.Divergence{Kind: domain.DuplicateInStatement, Statement: &st}, "1"); !errors.Is(err, domain.ErrNoAdjustment) {
		t.Fatalf("duplicate: err = %v", err)
	}
	le := entry("txn_x", domain.Credit, 3000, at(1))
	le.Currency = "USD"
	d := domain.Divergence{Kind: domain.AmountMismatch, Statement: &st, Ledger: &le}
	if _, err := domain.AdjustmentFor(d, "1"); !errors.Is(err, domain.ErrNoAdjustment) {
		t.Fatalf("currency: err = %v", err)
	}
}

func TestFourEyes(t *testing.T) {
	t.Parallel()
	if err := domain.CanApprove(domain.AdjustmentProposed, "ana", "ANA"); !errors.Is(err, domain.ErrSameOperator) {
		t.Fatalf("same operator: err = %v", err)
	}
	if err := domain.CanApprove(domain.AdjustmentApproved, "ana", "bruno"); !errors.Is(err, domain.ErrAdjustmentClosed) {
		t.Fatalf("closed: err = %v", err)
	}
	if err := domain.CanApprove(domain.AdjustmentProposed, "ana", "bruno"); err != nil {
		t.Fatal(err)
	}
	if err := domain.CanExplain(domain.CaseResolved); !errors.Is(err, domain.ErrCaseNotOpen) {
		t.Fatalf("explain resolved: err = %v", err)
	}
	if _, err := domain.RequireReason("   "); !errors.Is(err, domain.ErrReasonRequired) {
		t.Fatalf("reason: err = %v", err)
	}
}
