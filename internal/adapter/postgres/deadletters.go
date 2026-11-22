package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JohnnyMatteus/payments-events-go/internal/app"
)

// DeadLetterRepository implementa app.DeadLetterRepository.
type DeadLetterRepository struct {
	pool *pgxpool.Pool
}

// NewDeadLetterRepository cria o repositório da DLQ.
func NewDeadLetterRepository(pool *pgxpool.Pool) *DeadLetterRepository {
	return &DeadLetterRepository{pool: pool}
}

// Archive grava a mensagem morta. Se já há uma aberta com o mesmo message_id,
// soma as entregas em vez de duplicar a linha.
func (r *DeadLetterRepository) Archive(ctx context.Context, m app.ArchivedMessage) error {
	first := m.FirstFailedAt
	if first.IsZero() {
		first = time.Now().UTC()
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO dead_letters (message_id, routing_key, payload, reason, error, deliveries, first_failed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (message_id) WHERE status = 'open' DO UPDATE SET
			deliveries = dead_letters.deliveries + EXCLUDED.deliveries,
			error = EXCLUDED.error, archived_at = now()`,
		m.MessageID, m.RoutingKey, m.Payload, m.Reason, m.Error, m.Deliveries, first)
	if err != nil {
		return fmt.Errorf("archive dead letter %s: %w", m.MessageID, err)
	}
	return nil
}

// Requeue rearma no outbox a mensagem original, com o mesmo message_id.
func (r *DeadLetterRepository) Requeue(ctx context.Context, source app.DeadLetterSource, id int64, operator, note string) error {
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		messageID, err := lockOpen(ctx, tx, source, id)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			UPDATE outbox_messages SET status = 'pending', attempts = 0, next_attempt_at = now(), last_error = NULL,
				published_at = NULL, resolved_by = $2, resolved_at = now(), resolution_note = $3
			WHERE message_id = $1`, messageID, operator, note)
		if err != nil {
			return fmt.Errorf("rearm outbox message %s: %w", messageID, err)
		}
		if tag.RowsAffected() == 0 {
			// A linha publicada pode ter sido apagada pela retenção. Se o
			// evento consta como publicado e depois purgado, a mensagem é
			// recriada pendente, com o mesmo message_id, a partir da dead
			// letter (que guarda a rota e o corpo).
			if source != app.SourceBroker {
				return app.ErrNoOutboxOrigin
			}
			tag, err = tx.Exec(ctx, `
				INSERT INTO outbox_messages (message_id, routing_key, payload, headers, resolved_by, resolved_at, resolution_note)
				SELECT dl.message_id, dl.routing_key, convert_from(dl.payload, 'UTF8')::jsonb, '{}'::jsonb, $2, now(), $3
				FROM dead_letters dl
				WHERE dl.id = $1 AND EXISTS (
					SELECT 1 FROM processed_events pe WHERE pe.event_id = dl.message_id AND pe.outbox_purged_at IS NOT NULL)`,
				id, operator, note)
			if err != nil {
				return fmt.Errorf("recreate purged outbox message %s: %w", messageID, err)
			}
			if tag.RowsAffected() == 0 {
				return app.ErrNoOutboxOrigin
			}
		}
		return closeDeadLetter(ctx, tx, source, id, "requeued", operator, note)
	})
}

// Discard encerra a mensagem sem reprocessar.
func (r *DeadLetterRepository) Discard(ctx context.Context, source app.DeadLetterSource, id int64, operator, note string) error {
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		if _, err := lockOpen(ctx, tx, source, id); err != nil {
			return err
		}
		return closeDeadLetter(ctx, tx, source, id, "discarded", operator, note)
	})
}

func lockOpen(ctx context.Context, tx pgx.Tx, source app.DeadLetterSource, id int64) (string, error) {
	var messageID string
	var err error
	switch source {
	case app.SourceBroker:
		err = tx.QueryRow(ctx, `SELECT message_id FROM dead_letters WHERE id = $1 AND status = 'open' FOR UPDATE`, id).Scan(&messageID)
	case app.SourceOutbox:
		err = tx.QueryRow(ctx, `SELECT message_id FROM outbox_messages WHERE id = $1 AND status = 'dead' FOR UPDATE`, id).Scan(&messageID)
	default:
		return "", fmt.Errorf("%w: unknown source %q", app.ErrInvalidInput, source)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return "", app.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("lock dead letter: %w", err)
	}
	return messageID, nil
}

func closeDeadLetter(ctx context.Context, tx pgx.Tx, source app.DeadLetterSource, id int64, status, operator, note string) error {
	var err error
	if source == app.SourceBroker {
		_, err = tx.Exec(ctx, `
			UPDATE dead_letters SET status = $2, resolved_by = $3, resolved_at = now(), resolution_note = $4
			WHERE id = $1`, id, status, operator, note)
	} else if status == "discarded" {
		_, err = tx.Exec(ctx, `
			UPDATE outbox_messages SET status = 'discarded', resolved_by = $2, resolved_at = now(), resolution_note = $3
			WHERE id = $1`, id, operator, note)
	}
	if err != nil {
		return fmt.Errorf("close dead letter: %w", err)
	}
	return nil
}
