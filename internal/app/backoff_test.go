package app

import (
	"testing"
	"time"
)

func TestBackoffDelay(t *testing.T) {
	t.Parallel()
	b := Backoff{Base: time.Second, Max: 30 * time.Second}
	want := map[int]time.Duration{
		0: time.Second, 1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second,
		5: 16 * time.Second, 6: 30 * time.Second, 64: 30 * time.Second, 1000: 30 * time.Second,
	}
	for attempt, w := range want {
		if got := b.Delay(attempt); got != w {
			t.Errorf("Delay(%d) = %s, want %s", attempt, got, w)
		}
	}
}

func TestBackoffBaseAboveMaxIsCapped(t *testing.T) {
	t.Parallel()
	b := Backoff{Base: time.Minute, Max: time.Second}
	if got := b.Delay(1); got != time.Second {
		t.Fatalf("Delay(1) = %s, want 1s", got)
	}
}

func TestBackoffAppliesJitter(t *testing.T) {
	t.Parallel()
	b := Backoff{Base: time.Second, Max: time.Minute, Jitter: func(d time.Duration) time.Duration { return d / 4 }}
	if got := b.Delay(3); got != time.Second {
		t.Fatalf("Delay(3) with jitter = %s, want 1s", got)
	}
}

func TestEqualJitterStaysWithinBounds(t *testing.T) {
	t.Parallel()
	d := 10 * time.Second
	for range 1000 {
		got := EqualJitter(d)
		if got < d/2 || got > d {
			t.Fatalf("EqualJitter(%s) = %s, outside [d/2, d]", d, got)
		}
	}
	if got := EqualJitter(1); got != 1 {
		t.Fatalf("EqualJitter(1ns) = %s", got)
	}
}
