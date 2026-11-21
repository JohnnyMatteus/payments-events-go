package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JohnnyMatteus/payments-events-go/internal/app"
	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
)

const testSecret = "whsec_test"

type stubIngester struct {
	outcome app.IngestOutcome
	err     error
	got     []domain.Event
}

func (s *stubIngester) Ingest(_ context.Context, ev domain.Event, _ []byte) (app.IngestOutcome, error) {
	s.got = append(s.got, ev)
	return s.outcome, s.err
}

func newTestServer(t *testing.T, ing Ingester) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	v := newTestVerifier(t, testSecret)
	h, err := NewWebhookHandler(v, ing, logger)
	if err != nil {
		t.Fatal(err)
	}
	return NewRouter(h, http.NotFoundHandler(), func(context.Context) error { return nil })
}

func signedRequest(body string, ts time.Time) *http.Request {
	req := httptest.NewRequest(http.MethodPost, WebhookPath, strings.NewReader(body))
	req.Header.Set(SignatureHeader, Sign([]byte(testSecret), ts, []byte(body)))
	return req
}

const captureBody = `{"id":"evt_1","type":"payment.captured","created_at":"2026-11-20T12:00:00Z",
"data":{"transaction_id":"txn_1","payment_id":"pay_1","amount":15000,"currency":"BRL"},"extra":"ignored"}`

func TestWebhookResponses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		req        func() *http.Request
		ingester   *stubIngester
		wantStatus int
		wantBody   string
		wantCalls  int
	}{
		{
			name:     "novo evento",
			req:      func() *http.Request { return signedRequest(captureBody, sigNow) },
			ingester: &stubIngester{outcome: app.OutcomeRecorded}, wantStatus: http.StatusOK,
			wantBody: `"status":"recorded"`, wantCalls: 1,
		},
		{
			name:     "entrega duplicada devolve 200",
			req:      func() *http.Request { return signedRequest(captureBody, sigNow) },
			ingester: &stubIngester{outcome: app.OutcomeDuplicate}, wantStatus: http.StatusOK,
			wantBody: `"status":"duplicate"`, wantCalls: 1,
		},
		{
			name: "assinatura ausente",
			req: func() *http.Request {
				return httptest.NewRequest(http.MethodPost, WebhookPath, strings.NewReader(captureBody))
			},
			ingester: &stubIngester{}, wantStatus: http.StatusUnauthorized, wantBody: "invalid_signature",
		},
		{
			name:     "replay fora da janela",
			req:      func() *http.Request { return signedRequest(captureBody, sigNow.Add(-time.Hour)) },
			ingester: &stubIngester{}, wantStatus: http.StatusUnauthorized, wantBody: "stale_timestamp",
		},
		{
			name: "corpo adulterado após assinatura",
			req: func() *http.Request {
				req := signedRequest(captureBody, sigNow)
				req.Body = io.NopCloser(strings.NewReader(strings.Replace(captureBody, "15000", "99999", 1)))
				return req
			},
			ingester: &stubIngester{}, wantStatus: http.StatusUnauthorized,
		},
		{
			name:     "JSON inválido",
			req:      func() *http.Request { return signedRequest(`{"id":`, sigNow) },
			ingester: &stubIngester{}, wantStatus: http.StatusBadRequest, wantBody: "invalid_payload",
		},
		{
			name:       "evento viola contrato",
			req:        func() *http.Request { return signedRequest(captureBody, sigNow) },
			ingester:   &stubIngester{err: fmt.Errorf("%w: amount must be positive", domain.ErrInvalidEvent)},
			wantStatus: http.StatusUnprocessableEntity, wantBody: "amount must be positive", wantCalls: 1,
		},
		{
			name:     "falha de infraestrutura pede retry",
			req:      func() *http.Request { return signedRequest(captureBody, sigNow) },
			ingester: &stubIngester{err: errors.New("db down")}, wantStatus: http.StatusInternalServerError,
			wantBody: "internal_error", wantCalls: 1,
		},
		{
			name: "corpo acima do limite",
			req: func() *http.Request {
				return signedRequest(`{"pad":"`+strings.Repeat("x", MaxBodyBytes)+`"}`, sigNow)
			},
			ingester: &stubIngester{}, wantStatus: http.StatusRequestEntityTooLarge,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			newTestServer(t, tt.ingester).ServeHTTP(rec, tt.req())

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Fatalf("body %s does not contain %q", rec.Body, tt.wantBody)
			}
			if len(tt.ingester.got) != tt.wantCalls {
				t.Fatalf("ingester called %d times, want %d", len(tt.ingester.got), tt.wantCalls)
			}
			if rec.Code >= 400 && rec.Header().Get("Content-Type") != "application/problem+json" {
				t.Fatalf("error content type = %q", rec.Header().Get("Content-Type"))
			}
		})
	}
}

func TestWebhookMapsPayloadToDomainEvent(t *testing.T) {
	t.Parallel()
	ing := &stubIngester{outcome: app.OutcomeRecorded}
	rec := httptest.NewRecorder()
	newTestServer(t, ing).ServeHTTP(rec, signedRequest(captureBody, sigNow))

	want := domain.Event{
		ID: "evt_1", Type: domain.EventPaymentCaptured, TransactionID: "txn_1", PaymentID: "pay_1",
		AmountCents: 15000, Currency: "BRL", OccurredAt: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC),
	}
	if len(ing.got) != 1 || ing.got[0] != want {
		t.Fatalf("event = %+v, want %+v", ing.got, want)
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp["event_id"] != "evt_1" {
		t.Fatalf("response = %s (%v)", rec.Body, err)
	}
}

func TestRouterHealthAndReadiness(t *testing.T) {
	t.Parallel()
	failing := NewOpsRouter(http.NotFoundHandler(), func(context.Context) error { return errors.New("down") })
	ok := NewOpsRouter(http.NotFoundHandler(), func(context.Context) error { return nil })

	for _, tc := range []struct {
		h      http.Handler
		path   string
		status int
	}{
		{ok, "/healthz", http.StatusOK},
		{ok, "/readyz", http.StatusOK},
		{failing, "/healthz", http.StatusOK},
		{failing, "/readyz", http.StatusServiceUnavailable},
	} {
		rec := httptest.NewRecorder()
		tc.h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != tc.status {
			t.Errorf("%s = %d, want %d", tc.path, rec.Code, tc.status)
		}
	}

	rec := httptest.NewRecorder()
	newTestServer(t, &stubIngester{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, WebhookPath, nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET webhook = %d, want 405", rec.Code)
	}
}
