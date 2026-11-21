package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
)

// ReconcileService é o caso de uso do job de conciliação.
type ReconcileService struct {
	ledger LedgerReader
}

// NewReconcileService cria o caso de uso de conciliação.
func NewReconcileService(ledger LedgerReader) *ReconcileService {
	return &ReconcileService{ledger: ledger}
}

// Reconcile carrega os lançamentos do período e os compara com o extrato.
func (s *ReconcileService) Reconcile(ctx context.Context, period domain.Period,
	statement []domain.StatementLine) (domain.Report, error) {
	if !period.From.Before(period.To) {
		return domain.Report{}, errors.New("reconcile: period start must be before its end")
	}
	entries, err := s.ledger.EntriesBetween(ctx, period.From, period.To)
	if err != nil {
		return domain.Report{}, fmt.Errorf("load ledger entries: %w", err)
	}
	return domain.Reconcile(period, statement, entries), nil
}
