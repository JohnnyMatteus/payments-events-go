package app

import (
	"context"
	"errors"
	"testing"

	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
)

type fakeCache struct {
	state     ClaimState
	claimErr  error
	completed []string
	released  []string
}

func (f *fakeCache) Claim(context.Context, string) (ClaimState, error) { return f.state, f.claimErr }
func (f *fakeCache) Complete(_ context.Context, id string) error {
	f.completed = append(f.completed, id)
	return nil
}
func (f *fakeCache) Release(_ context.Context, id string) error {
	f.released = append(f.released, id)
	return nil
}

type fakeIngester struct {
	calls   int
	outcome IngestOutcome
	err     error
}

func (f *fakeIngester) Ingest(context.Context, domain.Event, []byte) (IngestOutcome, error) {
	f.calls++
	return f.outcome, f.err
}

func TestFastPathIngest(t *testing.T) {
	t.Parallel()
	errDB := errors.New("db down")
	tests := []struct {
		name          string
		cache         *fakeCache
		next          *fakeIngester
		wantOutcome   IngestOutcome
		wantErr       error
		wantCalls     int
		wantCompleted int
		wantReleased  int
	}{
		{"evento novo vai ao banco e marca como concluído", &fakeCache{state: ClaimAcquired},
			&fakeIngester{outcome: OutcomeRecorded}, OutcomeRecorded, nil, 1, 1, 0},
		{"reentrega concluída não toca no banco", &fakeCache{state: ClaimDone},
			&fakeIngester{}, OutcomeDuplicate, nil, 0, 0, 0},
		{"em andamento em outro processo: banco decide", &fakeCache{state: ClaimInFlight},
			&fakeIngester{outcome: OutcomeDuplicate}, OutcomeDuplicate, nil, 1, 0, 0},
		{"cache fora do ar: banco decide", &fakeCache{claimErr: errors.New("redis down")},
			&fakeIngester{outcome: OutcomeRecorded}, OutcomeRecorded, nil, 1, 0, 0},
		{"falha no banco libera o claim", &fakeCache{state: ClaimAcquired},
			&fakeIngester{err: errDB}, "", errDB, 1, 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := NewFastPathIngest(tt.next, tt.cache, discardLogger())
			got, err := f.Ingest(context.Background(), domain.Event{ID: "evt_1"}, nil)
			if !errors.Is(err, tt.wantErr) || got != tt.wantOutcome {
				t.Fatalf("got (%q, %v), want (%q, %v)", got, err, tt.wantOutcome, tt.wantErr)
			}
			if tt.next.calls != tt.wantCalls || len(tt.cache.completed) != tt.wantCompleted ||
				len(tt.cache.released) != tt.wantReleased {
				t.Fatalf("calls=%d completed=%v released=%v", tt.next.calls, tt.cache.completed, tt.cache.released)
			}
		})
	}
}
