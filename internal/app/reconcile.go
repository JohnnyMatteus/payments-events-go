package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
)

// ReconcileService é o caso de uso do job de conciliação.
type ReconcileService struct {
	statements StatementReader
	ledger     LedgerReader
}

// NewReconcileService cria o caso de uso de conciliação.
func NewReconcileService(statements StatementReader, ledger LedgerReader) *ReconcileService {
	return &ReconcileService{statements: statements, ledger: ledger}
}

// Reconcile lê o extrato indicado por ref, carrega os lançamentos do período
// e compara os dois lados.
func (s *ReconcileService) Reconcile(ctx context.Context, period domain.Period, ref string) (domain.Report, error) {
	if !period.From.Before(period.To) {
		return domain.Report{}, errors.New("reconcile: period start must be before its end")
	}
	statement, err := s.statements.ReadStatement(ctx, ref)
	if err != nil {
		return domain.Report{}, fmt.Errorf("read statement %s: %w", ref, err)
	}
	entries, err := s.ledger.EntriesBetween(ctx, period.From, period.To)
	if err != nil {
		return domain.Report{}, fmt.Errorf("load ledger entries: %w", err)
	}
	return domain.Reconcile(period, statement, entries), nil
}
