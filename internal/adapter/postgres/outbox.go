package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/JohnnyMatteus/payments-events-go/internal/app"
)

// maxErrorLength limita o texto de erro guardado por mensagem.
const maxErrorLength = 1000

// OutboxRepository implementa app.OutboxRepository.
type OutboxRepository struct {
	pool *pgxpool.Pool
}

// NewOutboxRepository cria o repositório do outbox.
func NewOutboxRepository(pool *pgxpool.Pool) *OutboxRepository {
	return &OutboxRepository{pool: pool}
}

// ProcessDue trava até limit mensagens vencidas com FOR UPDATE SKIP LOCKED,
// entrega cada uma ao handler e grava a decisão, tudo na mesma transação.
// Réplicas concorrentes do relay pegam lotes disjuntos. Se o processo morrer
// no meio do lote, a transação é desfeita e as mensagens voltam a ficar
// pendentes: a entrega é at-least-once e o consumidor precisa deduplicar.
func (r *OutboxRepository) ProcessDue(ctx context.Context, now time.Time, limit int,
	handle func(context.Context, app.OutboxMessage) app.Delivery) (int, error) {
	var processed int
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		msgs, err := lockDue(ctx, tx, now, limit)
		if err != nil {
			return err
		}
		for _, m := range msgs {
			msgCtx := otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier(m.Headers))
			msgCtx, span := tracer.Start(msgCtx, "outbox.deliver", trace.WithAttributes(
				attribute.String("messaging.message.id", m.MessageID),
				attribute.Int("outbox.attempts", m.Attempts),
			))
			d := handle(msgCtx, m)
			err := saveDecision(ctx, tx, m, d, now)
			span.End()
			if err != nil {
				return err
			}
			processed++
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("process due outbox messages: %w", err)
	}
	return processed, nil
}

func lockDue(ctx context.Context, tx pgx.Tx, now time.Time, limit int) ([]app.OutboxMessage, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, message_id, routing_key, payload, headers, attempts, created_at
		FROM outbox_messages
		WHERE status = 'pending' AND next_attempt_at <= $1
		ORDER BY next_attempt_at, id
		LIMIT $2
		FOR UPDATE SKIP LOCKED`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("select due messages: %w", err)
	}
	msgs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (app.OutboxMessage, error) {
		var (
			m       app.OutboxMessage
			headers []byte
		)
		if err := row.Scan(&m.ID, &m.MessageID, &m.RoutingKey, &m.Payload, &headers, &m.Attempts, &m.CreatedAt); err != nil {
			return m, err
		}
		if err := json.Unmarshal(headers, &m.Headers); err != nil {
			return m, fmt.Errorf("decode headers of message %s: %w", m.MessageID, err)
		}
		return m, nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan due messages: %w", err)
	}
	return msgs, nil
}

func saveDecision(ctx context.Context, tx pgx.Tx, m app.OutboxMessage, d app.Delivery, now time.Time) error {
	var err error
	switch d.Status {
	case app.DeliveryPublished:
		_, err = tx.Exec(ctx, `
			UPDATE outbox_messages SET status = 'published', published_at = $2, last_error = NULL
			WHERE id = $1`, m.ID, now)
	case app.DeliveryRetry:
		_, err = tx.Exec(ctx, `
			UPDATE outbox_messages SET attempts = attempts + 1, next_attempt_at = $2, last_error = $3
			WHERE id = $1`, m.ID, d.NextAttemptAt, errorText(d.Err))
	case app.DeliveryDead:
		_, err = tx.Exec(ctx, `
			UPDATE outbox_messages SET status = 'dead', attempts = attempts + 1, last_error = $2
			WHERE id = $1`, m.ID, errorText(d.Err))
	default:
		return fmt.Errorf("unknown delivery status %d for message %s", d.Status, m.MessageID)
	}
	if err != nil {
		return fmt.Errorf("update outbox message %s: %w", m.MessageID, err)
	}
	return nil
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if len(s) > maxErrorLength {
		s = s[:maxErrorLength]
	}
	return s
}
