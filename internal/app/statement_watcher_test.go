package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeClaims struct {
	seen     map[string]bool
	released []string
}

func (f *fakeClaims) Claim(_ context.Context, b, k, v string) (bool, error) {
	if f.seen == nil {
		f.seen = map[string]bool{}
	}
	id := b + "/" + k + "@" + v
	if f.seen[id] {
		return false, nil
	}
	f.seen[id] = true
	return true, nil
}

func (f *fakeClaims) Release(_ context.Context, b, k, v string) error {
	delete(f.seen, b+"/"+k+"@"+v)
	f.released = append(f.released, k)
	return nil
}

type fakeRunner struct {
	dates    []time.Time
	triggers []RunTrigger
	err      error
}

func (f *fakeRunner) Run(_ context.Context, d time.Time, t RunTrigger, _ string) (RunSummary, error) {
	f.dates = append(f.dates, d)
	f.triggers = append(f.triggers, t)
	return RunSummary{}, f.err
}

type fakeNotifier struct {
	batches [][]ObjectNotification
	acked   []string
	cancel  context.CancelFunc
}

func (f *fakeNotifier) Receive(_ context.Context) ([]ObjectNotification, error) {
	if len(f.batches) == 0 {
		f.cancel()
		return nil, nil
	}
	b := f.batches[0]
	f.batches = f.batches[1:]
	return b, nil
}

func (f *fakeNotifier) Ack(_ context.Context, n ObjectNotification) error {
	f.acked = append(f.acked, n.Receipt)
	return nil
}

func ev(key, version string) ObjectEvent {
	return ObjectEvent{Bucket: "b", Key: key, VersionID: version}
}

func TestWatcherReconcilesOncePerObjectVersion(t *testing.T) {
	runner, claims := &fakeRunner{}, &fakeClaims{}
	w := NewStatementWatcher(nil, claims, runner, discardLogger())
	for range 2 {
		if err := w.Handle(t.Context(), ev("settlements/2026-11-20.csv", "v1")); err != nil {
			t.Fatal(err)
		}
	}
	if len(runner.dates) != 1 || runner.triggers[0] != TriggerS3Notification {
		t.Fatalf("runs = %v %v, want one s3_notification run", runner.dates, runner.triggers)
	}
	if want := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC); !runner.dates[0].Equal(want) {
		t.Fatalf("date = %v, want %v", runner.dates[0], want)
	}
	// Uma nova versão do mesmo extrato concilia de novo.
	if err := w.Handle(t.Context(), ev("settlements/2026-11-20.csv", "v2")); err != nil || len(runner.dates) != 2 {
		t.Fatalf("new version: runs = %d, err = %v", len(runner.dates), err)
	}
}

func TestWatcherIgnoresForeignKeys(t *testing.T) {
	runner := &fakeRunner{}
	w := NewStatementWatcher(nil, &fakeClaims{}, runner, discardLogger())
	if err := w.Handle(t.Context(), ev("outros/arquivo.csv", "")); err != nil || len(runner.dates) != 0 {
		t.Fatalf("runs = %d, err = %v", len(runner.dates), err)
	}
}

func TestWatcherReleasesClaimWhenRunFails(t *testing.T) {
	boom := errors.New("ledger fora do ar")
	runner, claims := &fakeRunner{err: boom}, &fakeClaims{}
	w := NewStatementWatcher(nil, claims, runner, discardLogger())
	e := ev("settlements/2026-11-20.csv", "v1")
	if err := w.Handle(t.Context(), e); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	runner.err = nil
	if err := w.Handle(t.Context(), e); err != nil || len(runner.dates) != 2 {
		t.Fatalf("retry: runs = %d, err = %v", len(runner.dates), err)
	}
}

func TestWatcherAcksOnlySuccessfulNotifications(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	runner := &fakeRunner{}
	notifier := &fakeNotifier{cancel: cancel, batches: [][]ObjectNotification{{
		{Receipt: "ok", Events: []ObjectEvent{ev("settlements/2026-11-20.csv", "v1")}},
		{Receipt: "foreign", Events: []ObjectEvent{ev("x.txt", "")}},
	}}}
	w := NewStatementWatcher(notifier, &fakeClaims{}, runner, discardLogger())
	if err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(notifier.acked) != 2 {
		t.Fatalf("acked = %v, want both", notifier.acked)
	}

	ctx, cancel = context.WithCancel(t.Context())
	failing := &fakeRunner{err: errors.New("falha")}
	notifier = &fakeNotifier{cancel: cancel, batches: [][]ObjectNotification{{
		{Receipt: "bad", Events: []ObjectEvent{ev("settlements/2026-11-21.csv", "v1")}},
	}}}
	if err := NewStatementWatcher(notifier, &fakeClaims{}, failing, discardLogger()).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(notifier.acked) != 0 {
		t.Fatalf("acked = %v, failing notification must not be acked", notifier.acked)
	}
}
