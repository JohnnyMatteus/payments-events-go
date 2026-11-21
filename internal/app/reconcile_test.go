package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
)

type fakeLedger struct {
	entries  []domain.LedgerEntry
	err      error
	from, to time.Time
}

func (f *fakeLedger) EntriesBetween(_ context.Context, from, to time.Time) ([]domain.LedgerEntry, error) {
	f.from, f.to = from, to
	return f.entries, f.err
}

type fakeStatements struct {
	lines []domain.StatementLine
	err   error
	ref   string
}

func (f *fakeStatements) ReadStatement(_ context.Context, ref string) ([]domain.StatementLine, error) {
	f.ref = ref
	return f.lines, f.err
}

func TestReconcileServiceQueriesPeriodAndBuildsReport(t *testing.T) {
	t.Parallel()
	period := domain.Period{From: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)}
	period.To = period.From.AddDate(0, 0, 1)
	ledger := &fakeLedger{entries: []domain.LedgerEntry{{
		TransactionID: "txn_extra", Direction: domain.Credit, AmountCents: 10, Currency: "BRL",
		OccurredAt: period.From.Add(time.Hour),
	}}}

	statements := &fakeStatements{}
	report, err := NewReconcileService(statements, ledger).Reconcile(context.Background(), period, "s3://b/k.csv")
	if err != nil {
		t.Fatal(err)
	}
	if statements.ref != "s3://b/k.csv" {
		t.Fatalf("statement ref = %q", statements.ref)
	}
	if !ledger.from.Equal(period.From) || !ledger.to.Equal(period.To) {
		t.Fatalf("queried [%s, %s)", ledger.from, ledger.to)
	}
	if len(report.Divergences) != 1 || report.Divergences[0].Kind != domain.MissingInStatement {
		t.Fatalf("report = %+v", report)
	}
}

func TestReconcileServiceErrors(t *testing.T) {
	t.Parallel()
	day := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	valid := domain.Period{From: day, To: day.Add(time.Hour)}
	svc := NewReconcileService(&fakeStatements{}, &fakeLedger{})
	if _, err := svc.Reconcile(context.Background(), domain.Period{From: day, To: day}, "x"); err == nil {
		t.Fatal("empty period must fail")
	}
	notFound := errors.New("no such key")
	svc = NewReconcileService(&fakeStatements{err: notFound}, &fakeLedger{})
	if _, err := svc.Reconcile(context.Background(), valid, "x"); !errors.Is(err, notFound) {
		t.Fatalf("err = %v", err)
	}
	dbDown := errors.New("db down")
	svc = NewReconcileService(&fakeStatements{}, &fakeLedger{err: dbDown})
	if _, err := svc.Reconcile(context.Background(), valid, "x"); !errors.Is(err, dbDown) {
		t.Fatalf("err = %v", err)
	}
}
