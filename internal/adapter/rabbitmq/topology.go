// Package rabbitmq implementa a publicação (relay do outbox) e o consumo de
// eventos de integração sobre RabbitMQ (AMQP 0-9-1).
package rabbitmq

import (
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel/propagation"
)

// Topology descreve exchanges e filas usadas pelo serviço.
//
// A fila principal é quorum (replicada, durável) com x-delivery-limit: após
// DeliveryLimit reentregas o próprio broker manda a mensagem para a DLQ.
// Falhas permanentes vão direto para a DLQ via nack sem requeue.
type Topology struct {
	Exchange           string
	Queue              string
	BindingKey         string
	DeadLetterExchange string
	DeadLetterQueue    string
	DeliveryLimit      int
}

// DefaultTopology é a topologia do consumidor de exemplo.
func DefaultTopology() Topology {
	return Topology{
		Exchange:           "payments.events",
		Queue:              "balance-projector",
		BindingKey:         "ledger.#",
		DeadLetterExchange: "payments.events.dlx",
		DeadLetterQueue:    "balance-projector.dlq",
		DeliveryLimit:      5,
	}
}

// DeclareAll declara exchange, fila principal, DLX e DLQ. É idempotente e
// roda tanto no publisher quanto no consumidor, para que nenhuma mensagem
// seja publicada antes de existir uma fila ligada à exchange.
func (t Topology) DeclareAll(ch *amqp.Channel) error {
	if err := ch.ExchangeDeclare(t.Exchange, amqp.ExchangeTopic, true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare exchange %s: %w", t.Exchange, err)
	}
	if err := ch.ExchangeDeclare(t.DeadLetterExchange, amqp.ExchangeDirect, true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare dead letter exchange: %w", err)
	}
	if _, err := ch.QueueDeclare(t.DeadLetterQueue, true, false, false, false, amqp.Table{
		amqp.QueueTypeArg: amqp.QueueTypeQuorum,
	}); err != nil {
		return fmt.Errorf("declare dead letter queue: %w", err)
	}
	if err := ch.QueueBind(t.DeadLetterQueue, t.DeadLetterQueue, t.DeadLetterExchange, false, nil); err != nil {
		return fmt.Errorf("bind dead letter queue: %w", err)
	}
	if _, err := ch.QueueDeclare(t.Queue, true, false, false, false, amqp.Table{
		amqp.QueueTypeArg:           amqp.QueueTypeQuorum,
		"x-delivery-limit":          t.DeliveryLimit,
		"x-dead-letter-exchange":    t.DeadLetterExchange,
		"x-dead-letter-routing-key": t.DeadLetterQueue,
	}); err != nil {
		return fmt.Errorf("declare queue %s: %w", t.Queue, err)
	}
	if err := ch.QueueBind(t.Queue, t.BindingKey, t.Exchange, false, nil); err != nil {
		return fmt.Errorf("bind queue %s: %w", t.Queue, err)
	}
	return nil
}

// headerCarrier adapta amqp.Table ao propagador do OpenTelemetry.
type headerCarrier amqp.Table

var _ propagation.TextMapCarrier = headerCarrier{}

func (c headerCarrier) Get(key string) string {
	if v, ok := c[key].(string); ok {
		return v
	}
	return ""
}

func (c headerCarrier) Set(key, value string) { c[key] = value }

func (c headerCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}
