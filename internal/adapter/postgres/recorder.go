package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/JohnnyMatteus/payments-events-go/internal/app"
)

const instrumentationName = "github.com/JohnnyMatteus/payments-events-go/internal/adapter/postgres"

var tracer = otel.Tracer(instrumentationName)

// errDuplicateEvent interrompe a transação quando o event_id já existe.
var errDuplicateEvent = errors.New("duplicate event")

// EventRecorder implementa app.EventRecorder.
type EventRecorder struct {
	pool *pgxpool.Pool
}

// NewEventRecorder cria o gravador de eventos.
func NewEventRecorder(pool *pgxpool.Pool) *EventRecorder {
	return &EventRecorder{pool: pool}
}

// Record grava evento, lançamento e outbox numa única transação.
//
// A idempotência vem do INSERT ... ON CONFLICT DO NOTHING em processed_events:
// numa entrega concorrente do mesmo evento, a segunda transação espera o lock
// da chave única e, quando a primeira confirma, não insere nada. O resultado
// é "duplicate" e nenhuma outra tabela é tocada.
func (r *EventRecorder) Record(ctx context.Context, rec app.Recording) (outcome app.IngestOutcome, err error) {
	ctx, span := tracer.Start(ctx, "EventRecorder.Record", trace.WithAttributes(
		attribute.String("payment.event_id", rec.Event.ID),
		attribute.String("payment.event_type", string(rec.Event.Type)),
	))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.SetAttributes(attribute.String("payment.ingest_outcome", string(outcome)))
		span.End()
	}()

	payload := rec.RawPayload
	if len(payload) == 0 {
		payload = []byte("{}")
	}

	err = pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		outcome, err = r.record(ctx, tx, rec, payload)
		return err
	})
	if errors.Is(err, errDuplicateEvent) {
		return app.OutcomeDuplicate, nil
	}
	if err != nil {
		return "", fmt.Errorf("record event: %w", err)
	}
	return outcome, nil
}

func (r *EventRecorder) record(ctx context.Context, tx pgx.Tx, rec app.Recording, payload []byte) (app.IngestOutcome, error) {
	initial := app.OutcomeRecorded
	if rec.Entry == nil {
		initial = app.OutcomeIgnored
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO processed_events (event_id, event_type, outcome, payload)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (event_id) DO NOTHING`,
		rec.Event.ID, string(rec.Event.Type), string(initial), payload)
	if err != nil {
		return "", fmt.Errorf("insert processed event: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return "", errDuplicateEvent
	}
	if rec.Entry == nil {
		return app.OutcomeIgnored, nil
	}

	e := rec.Entry
	tag, err = tx.Exec(ctx, `
		INSERT INTO ledger_entries (event_id, transaction_id, payment_id, direction, amount_cents, currency, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (transaction_id) DO NOTHING`,
		e.EventID, e.TransactionID, e.PaymentID, string(e.Direction), e.AmountCents, string(e.Currency), e.OccurredAt)
	if err != nil {
		return "", fmt.Errorf("insert ledger entry: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Evento novo para uma transação já lançada: registra para auditoria,
		// mas não lança de novo nem publica.
		if _, err := tx.Exec(ctx, `UPDATE processed_events SET outcome = $2 WHERE event_id = $1`,
			rec.Event.ID, string(app.OutcomeDuplicateTransaction)); err != nil {
			return "", fmt.Errorf("mark duplicate transaction: %w", err)
		}
		return app.OutcomeDuplicateTransaction, nil
	}

	if err := insertOutbox(ctx, tx, rec.Message); err != nil {
		return "", err
	}
	return app.OutcomeRecorded, nil
}

func insertOutbox(ctx context.Context, tx pgx.Tx, msg *app.OutboxMessage) error {
	if msg == nil {
		return errors.New("monetary event without outbox message")
	}
	// O contexto de trace viaja com a mensagem para que o consumidor continue
	// o mesmo trace iniciado no webhook.
	headers := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, headers)
	for k, v := range msg.Headers {
		headers[k] = v
	}
	headersJSON, err := json.Marshal(headers)
	if err != nil {
		return fmt.Errorf("marshal outbox headers: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_messages (message_id, routing_key, payload, headers)
		VALUES ($1, $2, $3, $4)`,
		msg.MessageID, msg.RoutingKey, msg.Payload, headersJSON); err != nil {
		return fmt.Errorf("insert outbox message: %w", err)
	}
	return nil
}
