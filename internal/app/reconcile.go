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
	comparer   LedgerComparer
}

// LedgerComparer compara o extrato com o ledger do período no próprio banco,
// sem carregar os lançamentos na memória (volumes grandes). Deve devolver o
// mesmo Report que domain.Reconcile devolveria.
type LedgerComparer interface {
	Reconcile(ctx context.Context, period domain.Period, statement []domain.StatementLine) (domain.Report, error)
}

// WithComparer liga a comparação no banco no lugar da comparação em memória.
func (s *ReconcileService) WithComparer(c LedgerComparer) *ReconcileService {
	s.comparer = c
	return s
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
	if s.comparer != nil {
		report, err := s.comparer.Reconcile(ctx, period, statement)
		if err != nil {
			return domain.Report{}, fmt.Errorf("compare in database: %w", err)
		}
		return report, nil
	}
	entries, err := s.ledger.EntriesBetween(ctx, period.From, period.To)
	if err != nil {
		return domain.Report{}, fmt.Errorf("load ledger entries: %w", err)
	}
	return domain.Reconcile(period, statement, entries), nil
}
