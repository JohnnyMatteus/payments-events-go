package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JohnnyMatteus/payments-events-go/internal/app"
)

type stubLimiter struct {
	decision app.RateDecision
	err      error
	keys     []string
}

func (s *stubLimiter) Allow(_ context.Context, key string) (app.RateDecision, error) {
	s.keys = append(s.keys, key)
	return s.decision, s.err
}

func TestWebhookRateLimit(t *testing.T) {
	t.Parallel()
	body := strings.Replace(captureBody, `"data":{`, `"data":{"merchant_id":"loja_1",`, 1)
	tests := []struct {
		name       string
		limiter    *stubLimiter
		wantStatus int
		wantCalls  int
		wantRetry  string
	}{
		{"dentro do limite", &stubLimiter{decision: app.RateDecision{Allowed: true, Limit: 2, Remaining: 1}},
			http.StatusOK, 1, ""},
		{"acima do limite devolve 429", &stubLimiter{decision: app.RateDecision{Limit: 2, RetryAfter: 1500 * time.Millisecond}},
			http.StatusTooManyRequests, 0, "2"},
		{"limitador fora do ar deixa passar", &stubLimiter{err: errors.New("redis down")},
			http.StatusOK, 1, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ing := &stubIngester{outcome: app.OutcomeRecorded}
			h, err := NewWebhookHandler(newTestVerifier(t, testSecret), ing,
				slog.New(slog.NewTextHandler(io.Discard, nil)), WithRateLimiter(tt.limiter))
			if err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			NewRouter(h, http.NotFoundHandler(), func(context.Context) error { return nil }).
				ServeHTTP(rec, signedRequest(body, sigNow))
			if rec.Code != tt.wantStatus || len(ing.got) != tt.wantCalls {
				t.Fatalf("status=%d calls=%d, want %d/%d", rec.Code, len(ing.got), tt.wantStatus, tt.wantCalls)
			}
			if got := rec.Header().Get("Retry-After"); got != tt.wantRetry {
				t.Fatalf("Retry-After=%q, want %q", got, tt.wantRetry)
			}
			if len(tt.limiter.keys) != 1 || tt.limiter.keys[0] != "merchant:loja_1" {
				t.Fatalf("keys=%v", tt.limiter.keys)
			}
		})
	}
}
