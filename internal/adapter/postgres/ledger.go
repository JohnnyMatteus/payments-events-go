package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
)

// LedgerReader implementa app.LedgerReader.
type LedgerReader struct {
	pool *pgxpool.Pool
}

// NewLedgerReader cria o leitor do ledger.
func NewLedgerReader(pool *pgxpool.Pool) *LedgerReader {
	return &LedgerReader{pool: pool}
}

// EntriesBetween devolve os lançamentos com occurred_at em [from, to).
func (r *LedgerReader) EntriesBetween(ctx context.Context, from, to time.Time) ([]domain.LedgerEntry, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT event_id, transaction_id, payment_id, direction, amount_cents, currency, occurred_at
		FROM ledger_entries
		WHERE occurred_at >= $1 AND occurred_at < $2
		ORDER BY occurred_at, id`, from, to)
	if err != nil {
		return nil, fmt.Errorf("query ledger entries: %w", err)
	}
	entries, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.LedgerEntry, error) {
		var (
			e         domain.LedgerEntry
			direction string
			currency  string
		)
		err := row.Scan(&e.EventID, &e.TransactionID, &e.PaymentID, &direction, &e.AmountCents, &currency, &e.OccurredAt)
		e.Direction = domain.Direction(direction)
		e.Currency = domain.Currency(currency)
		e.OccurredAt = e.OccurredAt.UTC()
		return e, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan ledger entries: %w", err)
	}
	return entries, nil
}
