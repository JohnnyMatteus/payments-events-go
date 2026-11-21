package rabbitmq

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"

	"github.com/JohnnyMatteus/payments-events-go/internal/app"
)

// Publisher implementa app.Publisher com publisher confirms: Publish só
// retorna nil depois que o broker confirma que persistiu a mensagem.
// A conexão é aberta sob demanda e recriada após qualquer falha.
type Publisher struct {
	url            string
	topology       Topology
	confirmTimeout time.Duration

	mu   sync.Mutex
	conn *amqp.Connection
	ch   *amqp.Channel
}

// NewPublisher cria o publisher. Nenhuma conexão é aberta até o primeiro Publish.
func NewPublisher(url string, topology Topology, confirmTimeout time.Duration) *Publisher {
	return &Publisher{url: url, topology: topology, confirmTimeout: confirmTimeout}
}

// Publish envia a mensagem como persistente e espera o confirm do broker.
func (p *Publisher) Publish(ctx context.Context, msg app.OutboxMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	ch, err := p.channel()
	if err != nil {
		return err
	}

	headers := amqp.Table{}
	for k, v := range msg.Headers {
		headers[k] = v
	}
	otel.GetTextMapPropagator().Inject(ctx, headerCarrier(headers))

	ctx, cancel := context.WithTimeout(ctx, p.confirmTimeout)
	defer cancel()
	confirm, err := ch.PublishWithDeferredConfirmWithContext(ctx, p.topology.Exchange, msg.RoutingKey, false, false,
		amqp.Publishing{
			MessageId:    msg.MessageID,
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Timestamp:    time.Now().UTC(),
			Type:         msg.RoutingKey,
			Headers:      headers,
			Body:         msg.Payload,
		})
	if err != nil {
		p.reset()
		return fmt.Errorf("publish %s: %w", msg.MessageID, err)
	}
	acked, err := confirm.WaitContext(ctx)
	if err != nil {
		p.reset()
		return fmt.Errorf("wait confirm for %s: %w", msg.MessageID, err)
	}
	if !acked {
		return fmt.Errorf("broker nacked message %s", msg.MessageID)
	}
	return nil
}

// Close encerra canal e conexão.
func (p *Publisher) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn == nil {
		return nil
	}
	err := p.conn.Close()
	p.conn, p.ch = nil, nil
	if errors.Is(err, amqp.ErrClosed) {
		return nil
	}
	return err
}

func (p *Publisher) channel() (*amqp.Channel, error) {
	if p.ch != nil && !p.ch.IsClosed() {
		return p.ch, nil
	}
	p.reset()
	conn, err := amqp.Dial(p.url)
	if err != nil {
		return nil, fmt.Errorf("dial rabbitmq: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("open channel: %w", err)
	}
	if err := ch.Confirm(false); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("enable confirms: %w", err)
	}
	if err := p.topology.DeclareAll(ch); err != nil {
		_ = conn.Close()
		return nil, err
	}
	p.conn, p.ch = conn, ch
	return ch, nil
}

func (p *Publisher) reset() {
	if p.conn != nil {
		_ = p.conn.Close()
	}
	p.conn, p.ch = nil, nil
}
