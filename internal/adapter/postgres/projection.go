package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JohnnyMatteus/payments-events-go/internal/app"
	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
)

// ProjectionRepository implementa app.ProjectionRepository com o padrão
// inbox: o registro em consumed_messages e a atualização do saldo ficam na
// mesma transação, então reentregas do broker não somam duas vezes.
type ProjectionRepository struct {
	pool *pgxpool.Pool
}

// NewProjectionRepository cria o repositório da projeção de saldos.
func NewProjectionRepository(pool *pgxpool.Pool) *ProjectionRepository {
	return &ProjectionRepository{pool: pool}
}

// ApplyOnce aplica a mensagem se ela ainda não foi consumida por consumer.
func (r *ProjectionRepository) ApplyOnce(ctx context.Context, consumer, messageID string,
	msg app.LedgerEntryRecorded) (bool, error) {
	var applied bool
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			INSERT INTO consumed_messages (consumer, message_id) VALUES ($1, $2)
			ON CONFLICT DO NOTHING`, consumer, messageID)
		if err != nil {
			return fmt.Errorf("insert consumed message: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		captured, refunded := msg.AmountCents, int64(0)
		if msg.Direction == domain.Debit {
			captured, refunded = 0, msg.AmountCents
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO payment_balances (payment_id, currency, captured_cents, refunded_cents)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (payment_id) DO UPDATE SET
				captured_cents = payment_balances.captured_cents + EXCLUDED.captured_cents,
				refunded_cents = payment_balances.refunded_cents + EXCLUDED.refunded_cents,
				updated_at     = now()`,
			msg.PaymentID, string(msg.Currency), captured, refunded); err != nil {
			return fmt.Errorf("upsert payment balance: %w", err)
		}
		applied = true
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("apply message %s: %w", messageID, err)
	}
	return applied, nil
}
