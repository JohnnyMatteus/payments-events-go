package rabbitmq

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/JohnnyMatteus/payments-events-go/internal/app"
)

// requeueDelay evita um laço apertado quando o arquivamento falha.
const requeueDelay = 2 * time.Second

// ArchiveFunc registra uma mensagem morta.
type ArchiveFunc func(ctx context.Context, m app.ArchivedMessage) error

// DeadLetterConsumer lê a DLQ do broker e entrega cada mensagem ao
// arquivador, para a operação enxergá-las e tratá-las.
type DeadLetterConsumer struct {
	url      string
	topology Topology
	logger   *slog.Logger
}

// NewDeadLetterConsumer cria o consumidor da DLQ.
func NewDeadLetterConsumer(url string, topology Topology, logger *slog.Logger) *DeadLetterConsumer {
	return &DeadLetterConsumer{url: url, topology: topology, logger: logger}
}

// Run consome até o contexto ser cancelado.
func (c *DeadLetterConsumer) Run(ctx context.Context, archive ArchiveFunc) error {
	conn, err := amqp.Dial(c.url)
	if err != nil {
		return fmt.Errorf("dial rabbitmq: %w", err)
	}
	defer func() { _ = conn.Close() }()
	ch, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("open channel: %w", err)
	}
	if err := c.topology.DeclareAll(ch); err != nil {
		return err
	}
	if err := ch.Qos(5, 0, false); err != nil {
		return fmt.Errorf("set prefetch: %w", err)
	}
	deliveries, err := ch.ConsumeWithContext(ctx, c.topology.DeadLetterQueue, "", false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("consume %s: %w", c.topology.DeadLetterQueue, err)
	}
	c.logger.InfoContext(ctx, "dead letter consumer started", slog.String("queue", c.topology.DeadLetterQueue))
	for {
		select {
		case <-ctx.Done():
			return nil
		case d, ok := <-deliveries:
			if !ok {
				if ctx.Err() != nil {
					return nil
				}
				return errors.New("delivery channel closed by broker")
			}
			c.settle(ctx, d, archive)
		}
	}
}

func (c *DeadLetterConsumer) settle(ctx context.Context, d amqp.Delivery, archive ArchiveFunc) {
	msg := app.ArchivedMessage{
		MessageID: d.MessageId, RoutingKey: d.RoutingKey, Payload: d.Body,
		Reason: deathReason(d), Deliveries: deathCount(d), FirstFailedAt: deathTime(d),
	}
	if rk, ok := originalRoutingKey(d); ok {
		msg.RoutingKey = rk
	}
	if err := archive(ctx, msg); err != nil {
		c.logger.ErrorContext(ctx, "failed to archive dead letter",
			slog.String("message_id", d.MessageId), slog.Any("error", err))
		select {
		case <-ctx.Done():
		case <-time.After(requeueDelay):
		}
		_ = d.Nack(false, true)
		return
	}
	if err := d.Ack(false); err != nil {
		c.logger.ErrorContext(ctx, "failed to ack dead letter",
			slog.String("message_id", d.MessageId), slog.Any("error", err))
	}
}

// firstDeath devolve a entrada mais antiga do cabeçalho x-death, que o broker
// preenche ao encaminhar a mensagem para a DLX.
func firstDeath(d amqp.Delivery) amqp.Table {
	deaths, _ := d.Headers["x-death"].([]any)
	if len(deaths) == 0 {
		return nil
	}
	t, _ := deaths[len(deaths)-1].(amqp.Table)
	return t
}

// deathReason traduz o motivo do broker: rejected (consumidor recusou) ou
// delivery_limit (esgotou as reentregas).
func deathReason(d amqp.Delivery) string {
	switch r, _ := firstDeath(d)["reason"].(string); r {
	case "rejected":
		return "rejected"
	case "delivery_limit":
		return "delivery_limit"
	case "":
		return "unknown"
	default:
		return r
	}
}

func deathCount(d amqp.Delivery) int {
	if n, ok := firstDeath(d)["count"].(int64); ok && n > 0 {
		return int(n)
	}
	return 1
}

func deathTime(d amqp.Delivery) time.Time {
	if t, ok := firstDeath(d)["time"].(time.Time); ok {
		return t.UTC()
	}
	return time.Now().UTC()
}

func originalRoutingKey(d amqp.Delivery) (string, bool) {
	keys, _ := firstDeath(d)["routing-keys"].([]any)
	if len(keys) == 0 {
		return "", false
	}
	k, ok := keys[0].(string)
	return k, ok
}
