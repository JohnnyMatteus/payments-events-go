package rabbitmq

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/JohnnyMatteus/payments-events-go/internal/app"
)

const instrumentationName = "github.com/JohnnyMatteus/payments-events-go/internal/adapter/rabbitmq"

// Handler processa o corpo de uma mensagem. Erros que embrulham
// app.ErrPermanent mandam a mensagem direto para a DLQ.
type Handler func(ctx context.Context, messageID string, body []byte) error

// ConsumerConfig controla o consumo.
type ConsumerConfig struct {
	Prefetch int
	// Backoff define a espera antes de devolver uma mensagem com falha
	// transitória à fila, evitando reentregas em laço apertado.
	Backoff app.Backoff
}

// Consumer consome a fila principal com ack manual.
type Consumer struct {
	url      string
	topology Topology
	cfg      ConsumerConfig
	logger   *slog.Logger
	tracer   trace.Tracer
	outcomes metric.Int64Counter
}

// NewConsumer cria o consumidor e registra suas métricas.
func NewConsumer(url string, topology Topology, cfg ConsumerConfig, logger *slog.Logger) (*Consumer, error) {
	outcomes, err := otel.Meter(instrumentationName).Int64Counter("consumer_messages_total",
		metric.WithDescription("Mensagens consumidas por resultado (acked, requeued, dead_lettered)."))
	if err != nil {
		return nil, fmt.Errorf("create consumer metric: %w", err)
	}
	return &Consumer{
		url: url, topology: topology, cfg: cfg, logger: logger,
		tracer: otel.Tracer(instrumentationName), outcomes: outcomes,
	}, nil
}

// Run consome até o contexto ser cancelado. Se a conexão cair, retorna erro
// e deixa o reinício para o orquestrador (Docker, Kubernetes).
func (c *Consumer) Run(ctx context.Context, handle Handler) error {
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
	if err := ch.Qos(c.cfg.Prefetch, 0, false); err != nil {
		return fmt.Errorf("set prefetch: %w", err)
	}
	deliveries, err := ch.ConsumeWithContext(ctx, c.topology.Queue, "", false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("consume %s: %w", c.topology.Queue, err)
	}

	c.logger.InfoContext(ctx, "consumer started", slog.String("queue", c.topology.Queue))
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
			c.process(ctx, d, handle)
		}
	}
}

func (c *Consumer) process(ctx context.Context, d amqp.Delivery, handle Handler) {
	msgCtx := otel.GetTextMapPropagator().Extract(ctx, headerCarrier(d.Headers))
	msgCtx, span := c.tracer.Start(msgCtx, "consume "+d.RoutingKey,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.system", "rabbitmq"),
			attribute.String("messaging.destination.name", c.topology.Queue),
			attribute.String("messaging.message.id", d.MessageId),
		))
	defer span.End()

	err := handle(msgCtx, d.MessageId, d.Body)
	outcome, ackErr := c.settle(msgCtx, d, err)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	if ackErr != nil {
		c.logger.ErrorContext(msgCtx, "failed to settle message",
			slog.String("message_id", d.MessageId), slog.Any("error", ackErr))
	}
	c.outcomes.Add(msgCtx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
}

// settle decide entre ack, nack para a DLQ ou devolução à fila com espera.
func (c *Consumer) settle(ctx context.Context, d amqp.Delivery, err error) (string, error) {
	switch {
	case err == nil:
		return "acked", d.Ack(false)
	case app.IsPermanent(err):
		c.logger.ErrorContext(ctx, "permanent failure, sending to DLQ",
			slog.String("message_id", d.MessageId), slog.Any("error", err))
		return "dead_lettered", d.Nack(false, false)
	default:
		attempt := deliveryCount(d) + 1
		wait := c.cfg.Backoff.Delay(attempt)
		c.logger.WarnContext(ctx, "transient failure, requeueing",
			slog.String("message_id", d.MessageId), slog.Int("attempt", attempt),
			slog.Duration("wait", wait), slog.Any("error", err))
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
		return "requeued", d.Nack(false, true)
	}
}

// deliveryCount lê o contador de reentregas mantido pelas filas quorum.
func deliveryCount(d amqp.Delivery) int {
	switch v := d.Headers["x-delivery-count"].(type) {
	case int64:
		return int(v)
	case int32:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}
