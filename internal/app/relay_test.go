package app

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

var relayNow = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

func newTestRelay(t *testing.T, outbox OutboxRepository, pub Publisher, obs RelayObserver) *Relay {
	t.Helper()
	r, err := NewRelay(RelayConfig{
		BatchSize: 10, PollInterval: time.Millisecond, MaxAttempts: 3,
		Backoff: Backoff{Base: time.Second, Max: time.Minute},
	}, outbox, pub, obs, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	r.now = func() time.Time { return relayNow }
	return r
}

func TestRelayPublishesAndReschedulesWithBackoff(t *testing.T) {
	t.Parallel()
	outbox := &fakeOutbox{pending: []OutboxMessage{
		{MessageID: "ok", CreatedAt: relayNow.Add(-2 * time.Second)},
		{MessageID: "flaky", Attempts: 1},
		{MessageID: "dead", Attempts: 2},
	}}
	brokerDown := errors.New("broker unavailable")
	pub := &fakePublisher{failFor: map[string]error{"flaky": brokerDown, "dead": brokerDown}}
	obs := &recordingObserver{}

	n, err := newTestRelay(t, outbox, pub, obs).RunOnce(context.Background())
	if err != nil || n != 3 {
		t.Fatalf("RunOnce = %d, %v", n, err)
	}

	if d := outbox.decisions["ok"]; d.Status != DeliveryPublished {
		t.Fatalf("ok: %+v", d)
	}
	flaky := outbox.decisions["flaky"]
	if flaky.Status != DeliveryRetry || !errors.Is(flaky.Err, brokerDown) {
		t.Fatalf("flaky: %+v", flaky)
	}
	// Segunda tentativa: atraso de Base*2 = 2s.
	if want := relayNow.Add(2 * time.Second); !flaky.NextAttemptAt.Equal(want) {
		t.Fatalf("flaky next attempt = %s, want %s", flaky.NextAttemptAt, want)
	}
	if d := outbox.decisions["dead"]; d.Status != DeliveryDead || !errors.Is(d.Err, brokerDown) {
		t.Fatalf("dead: %+v", d)
	}
	if obs.published != 1 || obs.dead != 1 || len(obs.retries) != 1 || obs.retries[0] != 2 {
		t.Fatalf("observer = %+v", obs)
	}
	if obs.lags[0] != 2*time.Second {
		t.Fatalf("lag = %s, want 2s", obs.lags[0])
	}
}

func TestRelayRunOnceWrapsRepositoryError(t *testing.T) {
	t.Parallel()
	dbDown := errors.New("db down")
	_, err := newTestRelay(t, &fakeOutbox{err: dbDown}, &fakePublisher{}, &recordingObserver{}).RunOnce(context.Background())
	if !errors.Is(err, dbDown) {
		t.Fatalf("err = %v", err)
	}
}

func TestRelayRunDrainsBacklogAndStopsOnCancel(t *testing.T) {
	t.Parallel()
	pending := make([]OutboxMessage, 25)
	for i := range pending {
		pending[i] = OutboxMessage{MessageID: string(rune('a' + i))}
	}
	outbox := &fakeOutbox{pending: pending}
	pub := &fakePublisher{}
	relay := newTestRelay(t, outbox, pub, &recordingObserver{})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := relay.Run(ctx); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if len(pub.sent) != 25 {
		t.Fatalf("published %d messages, want 25", len(pub.sent))
	}
}

func TestNewRelayValidatesConfig(t *testing.T) {
	t.Parallel()
	_, err := NewRelay(RelayConfig{}, &fakeOutbox{}, &fakePublisher{}, &recordingObserver{}, discardLogger())
	if err == nil {
		t.Fatal("expected error for zero config")
	}
}

type countingOutbox struct{ calls atomic.Int32 }

func (c *countingOutbox) ProcessDue(context.Context, time.Time, int, func(context.Context, OutboxMessage) Delivery) (int, error) {
	c.calls.Add(1)
	return 0, nil
}

// Com o polling longo, só a notificação do banco faz o relay tentar de novo: a
// latência não depende do intervalo de polling.
func TestRelayWakesOnNotificationBeforeThePollInterval(t *testing.T) {
	t.Parallel()
	wake := make(chan struct{}, 1)
	outbox := &countingOutbox{}
	relay, err := NewRelay(RelayConfig{
		BatchSize: 10, PollInterval: time.Hour, MaxAttempts: 3,
		Backoff: Backoff{Base: time.Second, Max: time.Minute}, Wake: wake,
	}, outbox, &fakePublisher{}, &recordingObserver{}, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- relay.Run(ctx) }()
	waitCalls := func(want int32) {
		t.Helper()
		for range 200 {
			if outbox.calls.Load() >= want {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("o relay chamou ProcessDue %d vezes, want pelo menos %d", outbox.calls.Load(), want)
	}
	waitCalls(1) // ciclo inicial
	wake <- struct{}{}
	waitCalls(2)
	wake <- struct{}{}
	waitCalls(3)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run = %v", err)
	}
}
