//go:build integration

package rabbitmq_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/rabbitmq"
	"github.com/JohnnyMatteus/payments-events-go/internal/app"
	"github.com/JohnnyMatteus/payments-events-go/internal/testinfra"
)

var amqpURL string

func TestMain(m *testing.M) {
	os.Exit(testinfra.Run(m, func(ctx context.Context) (func(), error) {
		url, teardown, err := testinfra.StartRabbitMQ(ctx)
		amqpURL = url
		return teardown, err
	}))
}

// uniqueTopology isola cada teste em filas próprias.
func uniqueTopology(t *testing.T) rabbitmq.Topology {
	t.Helper()
	top := rabbitmq.DefaultTopology()
	suffix := fmt.Sprintf(".%s.%d", t.Name(), time.Now().UnixNano())
	top.Exchange += suffix
	top.Queue += suffix
	top.DeadLetterExchange += suffix
	top.DeadLetterQueue += suffix
	top.DeliveryLimit = 2
	return top
}

func publish(t *testing.T, top rabbitmq.Topology, id string) {
	t.Helper()
	pub := rabbitmq.NewPublisher(amqpURL, top, 5*time.Second)
	t.Cleanup(func() { _ = pub.Close() })
	err := pub.Publish(context.Background(), app.OutboxMessage{
		MessageID: id, RoutingKey: app.RoutingKeyLedgerEntryRecorded, Payload: []byte(`{"n":1}`),
		Headers: map[string]string{"x-origin": "test"},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
}

func runConsumer(t *testing.T, top rabbitmq.Topology, handle rabbitmq.Handler) context.CancelFunc {
	t.Helper()
	c, err := rabbitmq.NewConsumer(amqpURL, top, rabbitmq.ConsumerConfig{
		Prefetch: 1, Backoff: app.Backoff{Base: 10 * time.Millisecond, Max: 50 * time.Millisecond},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, handle) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("consumer Run: %v", err)
		}
	})
	return cancel
}

func dlqMessage(t *testing.T, top rabbitmq.Topology) (amqp.Delivery, bool) {
	t.Helper()
	conn, err := amqp.Dial(amqpURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		d, ok, err := ch.Get(top.DeadLetterQueue, true)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			return d, true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return amqp.Delivery{}, false
}

func TestPublishAndConsumeWithAck(t *testing.T) {
	top := uniqueTopology(t)
	received := make(chan string, 1)
	runConsumer(t, top, func(_ context.Context, id string, body []byte) error {
		received <- id + " " + string(body)
		return nil
	})
	time.Sleep(200 * time.Millisecond) // garante a topologia antes da publicação
	publish(t, top, "evt_ack")

	select {
	case got := <-received:
		if got != `evt_ack {"n":1}` {
			t.Fatalf("received %q", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("message not consumed")
	}
}

func TestPermanentFailureGoesStraightToDLQ(t *testing.T) {
	top := uniqueTopology(t)
	var calls int
	var mu sync.Mutex
	runConsumer(t, top, func(context.Context, string, []byte) error {
		mu.Lock()
		calls++
		mu.Unlock()
		return fmt.Errorf("%w: bad payload", app.ErrPermanent)
	})
	time.Sleep(200 * time.Millisecond)
	publish(t, top, "evt_poison")

	d, ok := dlqMessage(t, top)
	if !ok || d.MessageId != "evt_poison" {
		t.Fatalf("DLQ message = %+v, ok=%v", d.MessageId, ok)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("handler called %d times, want 1 (no retry for permanent failures)", calls)
	}
}

func TestTransientFailureIsRetriedThenDeadLettered(t *testing.T) {
	top := uniqueTopology(t)
	var calls int
	var mu sync.Mutex
	runConsumer(t, top, func(context.Context, string, []byte) error {
		mu.Lock()
		calls++
		mu.Unlock()
		return errors.New("database unavailable")
	})
	time.Sleep(200 * time.Millisecond)
	publish(t, top, "evt_flaky")

	d, ok := dlqMessage(t, top)
	if !ok || d.MessageId != "evt_flaky" {
		t.Fatalf("DLQ message = %+v, ok=%v", d.MessageId, ok)
	}
	if d.Headers["x-origin"] != "test" {
		t.Fatalf("custom headers lost: %v", d.Headers)
	}
	mu.Lock()
	defer mu.Unlock()
	// Primeira entrega + DeliveryLimit (2) reentregas.
	if calls != 3 {
		t.Fatalf("handler called %d times, want 3", calls)
	}
}

func TestPublisherReconnectsAfterBrokerClosesConnection(t *testing.T) {
	top := uniqueTopology(t)
	pub := rabbitmq.NewPublisher(amqpURL, top, 5*time.Second)
	defer pub.Close()
	msg := app.OutboxMessage{MessageID: "evt_1", RoutingKey: app.RoutingKeyLedgerEntryRecorded, Payload: []byte(`{}`)}
	if err := pub.Publish(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if err := pub.Close(); err != nil {
		t.Fatal(err)
	}
	msg.MessageID = "evt_2"
	if err := pub.Publish(context.Background(), msg); err != nil {
		t.Fatalf("publish after close should reconnect: %v", err)
	}
}

func TestPublisherFailsWhenBrokerUnreachable(t *testing.T) {
	pub := rabbitmq.NewPublisher("amqp://guest:guest@127.0.0.1:1/", uniqueTopology(t), time.Second)
	err := pub.Publish(context.Background(), app.OutboxMessage{MessageID: "x", RoutingKey: "ledger.x"})
	if err == nil {
		t.Fatal("expected error for unreachable broker")
	}
}
