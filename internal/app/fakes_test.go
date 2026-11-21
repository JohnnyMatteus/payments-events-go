package app

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"time"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type fakeRecorder struct {
	got     []Recording
	outcome IngestOutcome
	err     error
}

func (f *fakeRecorder) Record(_ context.Context, rec Recording) (IngestOutcome, error) {
	f.got = append(f.got, rec)
	return f.outcome, f.err
}

// fakeOutbox entrega as mensagens configuradas e guarda a decisão do relay.
type fakeOutbox struct {
	pending   []OutboxMessage
	decisions map[string]Delivery
	err       error
}

func (f *fakeOutbox) ProcessDue(ctx context.Context, _ time.Time, limit int,
	handle func(context.Context, OutboxMessage) Delivery) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	if f.decisions == nil {
		f.decisions = map[string]Delivery{}
	}
	n := min(limit, len(f.pending))
	for _, m := range f.pending[:n] {
		f.decisions[m.MessageID] = handle(ctx, m)
	}
	f.pending = f.pending[n:]
	return n, nil
}

type fakePublisher struct {
	failFor map[string]error
	sent    []string
}

func (f *fakePublisher) Publish(_ context.Context, msg OutboxMessage) error {
	if err := f.failFor[msg.MessageID]; err != nil {
		return err
	}
	f.sent = append(f.sent, msg.MessageID)
	return nil
}

type recordingObserver struct {
	published, dead int
	retries         []int
	lags            []time.Duration
}

func (o *recordingObserver) Published(lag time.Duration) { o.published++; o.lags = append(o.lags, lag) }
func (o *recordingObserver) Retried(attempt int)         { o.retries = append(o.retries, attempt) }
func (o *recordingObserver) DeadLettered()               { o.dead++ }

type fakeProjection struct {
	mu      sync.Mutex
	applied map[string]LedgerEntryRecorded
	err     error
}

func (f *fakeProjection) ApplyOnce(_ context.Context, consumer, messageID string, msg LedgerEntryRecorded) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return false, f.err
	}
	if f.applied == nil {
		f.applied = map[string]LedgerEntryRecorded{}
	}
	key := consumer + "/" + messageID
	if _, ok := f.applied[key]; ok {
		return false, nil
	}
	f.applied[key] = msg
	return true, nil
}
