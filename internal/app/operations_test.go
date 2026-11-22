package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
)

type fakeDeadLetters struct {
	requeued  []string
	discarded []string
	archived  []ArchivedMessage
}

func (f *fakeDeadLetters) Archive(_ context.Context, m ArchivedMessage) error {
	f.archived = append(f.archived, m)
	return nil
}

func (f *fakeDeadLetters) Requeue(_ context.Context, _ DeadLetterSource, _ int64, operator, note string) error {
	f.requeued = append(f.requeued, operator+":"+note)
	return nil
}

func (f *fakeDeadLetters) Discard(_ context.Context, _ DeadLetterSource, _ int64, operator, note string) error {
	f.discarded = append(f.discarded, operator+":"+note)
	return nil
}

func TestDeadLetterServiceRequiresReason(t *testing.T) {
	repo := &fakeDeadLetters{}
	svc := NewDeadLetterService(repo)
	if err := svc.Reprocess(t.Context(), SourceBroker, 1, "ana", "  "); !errors.Is(err, domain.ErrReasonRequired) {
		t.Fatalf("Reprocess without reason = %v, want ErrReasonRequired", err)
	}
	if err := svc.Discard(t.Context(), SourceBroker, 1, "ana", ""); !errors.Is(err, domain.ErrReasonRequired) {
		t.Fatalf("Discard without reason = %v, want ErrReasonRequired", err)
	}
	if len(repo.requeued)+len(repo.discarded) != 0 {
		t.Fatal("repository must not be called without a reason")
	}
	if err := svc.Reprocess(t.Context(), SourceBroker, 1, "ana", " banco voltou "); err != nil {
		t.Fatalf("Reprocess: %v", err)
	}
	if got := repo.requeued; len(got) != 1 || got[0] != "ana:banco voltou" {
		t.Fatalf("requeued = %v", got)
	}
}

func TestArchiverDiagnosesPoisonMessage(t *testing.T) {
	repo := &fakeDeadLetters{}
	a := NewDeadLetterArchiver(repo)
	if err := a.Archive(t.Context(), ArchivedMessage{MessageID: "m1", Payload: []byte(`{"schema_version":9}`)}); err != nil {
		t.Fatal(err)
	}
	if err := a.Archive(t.Context(), ArchivedMessage{MessageID: "m2", Payload: validLedgerMessage()}); err != nil {
		t.Fatal(err)
	}
	if got := repo.archived[0].Error; !strings.Contains(got, "schema_version 9") {
		t.Errorf("poison error = %q", got)
	}
	if got := repo.archived[1].Error; !strings.Contains(got, "transitória") {
		t.Errorf("transient error = %q", got)
	}
	if repo.archived[1].Deliveries != 1 {
		t.Errorf("deliveries = %d, want 1", repo.archived[1].Deliveries)
	}
}

func validLedgerMessage() []byte {
	return []byte(`{"schema_version":1,"event_id":"e","transaction_id":"t","payment_id":"p","direction":"credit","amount_cents":100,"currency":"BRL"}`)
}

// fakeCases executa as regras de decide como o repositório real faz, sob a
// linha travada.
type fakeCases struct {
	CaseRepository
	c   Case
	adj Adjustment
}

func (f *fakeCases) Explain(_ context.Context, _ int64, decide func(Case) error, _, reason string) error {
	if err := decide(f.c); err != nil {
		return err
	}
	f.c.Status, f.c.Explanation = domain.CaseExplained, reason
	return nil
}

func (f *fakeCases) ProposeAdjustment(_ context.Context, _ int64, build func(Case) (domain.LedgerEntry, error),
	operator, reason string) (Adjustment, error) {
	entry, err := build(f.c)
	if err != nil {
		return Adjustment{}, err
	}
	f.adj = Adjustment{ID: 7, CaseID: f.c.ID, Entry: entry, Reason: reason, Status: domain.AdjustmentProposed, ProposedBy: operator}
	return f.adj, nil
}

func (f *fakeCases) ApproveAdjustment(_ context.Context, _ int64, decide func(Adjustment) (Recording, error),
	_, _ string) (Adjustment, error) {
	if _, err := decide(f.adj); err != nil {
		return Adjustment{}, err
	}
	f.adj.Status = domain.AdjustmentApproved
	return f.adj, nil
}

func mismatchCase() Case {
	at := time.Date(2026, 11, 20, 13, 0, 0, 0, time.UTC)
	return Case{
		ID: 3, Status: domain.CaseOpen, Kind: domain.AmountMismatch,
		Divergence: domain.Divergence{
			Kind:      domain.AmountMismatch,
			Statement: &domain.StatementLine{TransactionID: "tx2", PaymentID: "p1", Direction: domain.Debit, AmountCents: 3000, Currency: "BRL", OccurredAt: at},
			Ledger: &domain.LedgerEntry{MerchantID: "loja", TransactionID: "tx2", PaymentID: "p1", Direction: domain.Debit,
				AmountCents: 2500, Currency: "BRL", OccurredAt: at},
		},
	}
}

func TestCaseServiceFourEyes(t *testing.T) {
	repo := &fakeCases{c: mismatchCase()}
	svc := NewCaseService(repo)
	adj, err := svc.ProposeAdjustment(t.Context(), 3, "ana", "provedor cobrou taxa")
	if err != nil {
		t.Fatalf("ProposeAdjustment: %v", err)
	}
	// extrato -30,00 e ledger -25,00: faltam 5,00 de débito, sem tocar no lançamento original.
	if adj.Entry.Direction != domain.Debit || adj.Entry.AmountCents != 500 || adj.Entry.AdjustsTransactionID != "tx2" {
		t.Fatalf("adjustment entry = %+v", adj.Entry)
	}
	if _, err := svc.ApproveAdjustment(t.Context(), 7, "ANA", ""); !errors.Is(err, domain.ErrSameOperator) {
		t.Fatalf("self approval = %v, want ErrSameOperator", err)
	}
	if _, err := svc.ApproveAdjustment(t.Context(), 7, "bruno", "ok"); err != nil {
		t.Fatalf("approval by another operator: %v", err)
	}
	if _, err := svc.ApproveAdjustment(t.Context(), 7, "carla", ""); !errors.Is(err, domain.ErrAdjustmentClosed) {
		t.Fatalf("second approval = %v, want ErrAdjustmentClosed", err)
	}
}

func TestCaseServiceExplainNeedsOpenCaseAndReason(t *testing.T) {
	repo := &fakeCases{c: mismatchCase()}
	svc := NewCaseService(repo)
	if err := svc.Explain(t.Context(), 3, "ana", ""); !errors.Is(err, domain.ErrReasonRequired) {
		t.Fatalf("Explain without reason = %v", err)
	}
	if err := svc.Explain(t.Context(), 3, "ana", "liquida amanhã"); err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if err := svc.Explain(t.Context(), 3, "ana", "de novo"); !errors.Is(err, domain.ErrCaseNotOpen) {
		t.Fatalf("Explain on closed case = %v, want ErrCaseNotOpen", err)
	}
}

func TestItemsOfKeepsStatementOrderAndPutsLedgerOnlyLast(t *testing.T) {
	line := func(n int, tx string) *domain.StatementLine {
		return &domain.StatementLine{LineNumber: n, TransactionID: tx}
	}
	report := domain.Report{
		Matches: []domain.Match{{Statement: *line(3, "c")}, {Statement: *line(1, "a")}},
		Divergences: []domain.Divergence{
			{Kind: domain.MissingInStatement, TransactionID: "z", Ledger: &domain.LedgerEntry{}},
			{Kind: domain.MissingInLedger, TransactionID: "b", Statement: line(2, "b")},
		},
	}
	var got []string
	for _, it := range ItemsOf(report) {
		got = append(got, it.TransactionID)
	}
	if strings.Join(got, ",") != "a,b,c,z" {
		t.Fatalf("order = %v", got)
	}
}

func TestSettlementKeyRoundTrip(t *testing.T) {
	date := time.Date(2026, 11, 20, 23, 0, 0, 0, time.UTC)
	key := SettlementKey(date)
	if key != "settlements/2026-11-20.csv" {
		t.Fatalf("key = %q", key)
	}
	if got, ok := SettlementDateFromKey(key); !ok || got.Format(time.DateOnly) != "2026-11-20" {
		t.Fatalf("round trip = %v %v", got, ok)
	}
	for _, bad := range []string{"settlements/x.csv", "other/2026-11-20.csv", "settlements/2026-11-20.txt"} {
		if _, ok := SettlementDateFromKey(bad); ok {
			t.Errorf("%q must be rejected", bad)
		}
	}
}
