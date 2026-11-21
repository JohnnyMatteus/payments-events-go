// Package httpapi é o adaptador HTTP de entrada: webhook do provedor,
// health checks e exposição de métricas.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/JohnnyMatteus/payments-events-go/internal/app"
	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
)

const instrumentationName = "github.com/JohnnyMatteus/payments-events-go/internal/adapter/httpapi"

// MaxBodyBytes limita o tamanho do corpo aceito no webhook.
const MaxBodyBytes = 1 << 20

// Ingester é a porta de entrada usada pelo handler.
type Ingester = app.Ingester

// webhookPayload é o contrato JSON do webhook do PagaFácil Sandbox.
// Campos desconhecidos são ignorados (tolerant reader).
type webhookPayload struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"created_at"`
	Data      struct {
		MerchantID    string `json:"merchant_id"`
		TransactionID string `json:"transaction_id"`
		PaymentID     string `json:"payment_id"`
		Amount        int64  `json:"amount"`
		Currency      string `json:"currency"`
	} `json:"data"`
}

func (p webhookPayload) toEvent() domain.Event {
	return domain.Event{
		ID:            p.ID,
		Type:          domain.EventType(p.Type),
		MerchantID:    p.Data.MerchantID,
		TransactionID: p.Data.TransactionID,
		PaymentID:     p.Data.PaymentID,
		AmountCents:   p.Data.Amount,
		Currency:      domain.Currency(p.Data.Currency),
		OccurredAt:    p.CreatedAt,
	}
}

// WebhookHandler recebe eventos do provedor.
type WebhookHandler struct {
	verifier *Verifier
	ingester Ingester
	limiter  app.RateLimiter
	logger   *slog.Logger
	requests metric.Int64Counter
}

// WebhookOption configura dependências opcionais do handler.
type WebhookOption func(*WebhookHandler)

// WithRateLimiter limita as requisições por lojista (data.merchant_id).
func WithRateLimiter(l app.RateLimiter) WebhookOption {
	return func(h *WebhookHandler) { h.limiter = l }
}

// NewWebhookHandler cria o handler e registra a métrica webhook_requests_total.
func NewWebhookHandler(verifier *Verifier, ingester Ingester, logger *slog.Logger,
	opts ...WebhookOption) (*WebhookHandler, error) {
	requests, err := otel.Meter(instrumentationName).Int64Counter("webhook_requests_total",
		metric.WithDescription("Webhooks recebidos por resultado."))
	if err != nil {
		return nil, err
	}
	h := &WebhookHandler{verifier: verifier, ingester: ingester, logger: logger, requests: requests}
	for _, opt := range opts {
		opt(h)
	}
	return h, nil
}

// ServeHTTP implementa o fluxo: limitar corpo, verificar assinatura e janela
// de tempo, decodificar, ingerir. Qualquer resposta 2xx encerra as tentativas
// do provedor; 5xx pede nova tentativa.
func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			h.reject(ctx, w, http.StatusRequestEntityTooLarge, "payload_too_large", "corpo maior que o limite", err)
			return
		}
		h.reject(ctx, w, http.StatusBadRequest, "unreadable_body", "não foi possível ler o corpo", err)
		return
	}

	if err := h.verifier.Verify(r.Header.Get(SignatureHeader), body); err != nil {
		outcome := "invalid_signature"
		if errors.Is(err, ErrStaleTimestamp) {
			outcome = "stale_timestamp"
		}
		// A causa exata vai só para o log: o cliente não aprende o que falhou.
		h.reject(ctx, w, http.StatusUnauthorized, outcome, "assinatura inválida", err)
		return
	}

	var payload webhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		h.reject(ctx, w, http.StatusBadRequest, "invalid_payload", "JSON inválido", err)
		return
	}
	ev := payload.toEvent()
	trace.SpanFromContext(ctx).SetAttributes(
		attribute.String("payment.event_id", ev.ID),
		attribute.String("payment.event_type", string(ev.Type)),
		attribute.String("payment.merchant_id", payload.Data.MerchantID),
	)
	// O limite vem depois da assinatura: requisições forjadas não consomem
	// a cota de um lojista.
	if !h.allow(ctx, w, payload.Data.MerchantID) {
		return
	}

	outcome, err := h.ingester.Ingest(ctx, ev, body)
	switch {
	case errors.Is(err, domain.ErrInvalidEvent):
		h.reject(ctx, w, http.StatusUnprocessableEntity, "invalid_event", err.Error(), err)
		return
	case err != nil:
		h.logger.ErrorContext(ctx, "webhook ingestion failed", slog.String("event_id", ev.ID), slog.Any("error", err))
		h.count(ctx, "error")
		writeProblem(w, http.StatusInternalServerError, "internal_error", "falha temporária, tente novamente")
		return
	}

	h.count(ctx, string(outcome))
	h.logger.InfoContext(ctx, "webhook processed",
		slog.String("event_id", ev.ID), slog.String("event_type", string(ev.Type)), slog.String("outcome", string(outcome)))
	writeJSON(w, http.StatusOK, map[string]string{"event_id": ev.ID, "status": string(outcome)})
}

// allow aplica o limite por lojista. Se o limitador falhar, a requisição
// segue: perder o limite é melhor que recusar pagamentos.
func (h *WebhookHandler) allow(ctx context.Context, w http.ResponseWriter, merchantID string) bool {
	if h.limiter == nil {
		return true
	}
	if merchantID == "" {
		merchantID = "unknown"
	}
	decision, err := h.limiter.Allow(ctx, "merchant:"+merchantID)
	if err != nil {
		h.logger.WarnContext(ctx, "rate limiter unavailable, allowing request", slog.Any("error", err))
		return true
	}
	w.Header().Set("RateLimit-Limit", strconv.Itoa(decision.Limit))
	w.Header().Set("RateLimit-Remaining", strconv.Itoa(decision.Remaining))
	if decision.Allowed {
		return true
	}
	retry := max(int(decision.RetryAfter.Round(time.Second).Seconds()), 1)
	w.Header().Set("Retry-After", strconv.Itoa(retry))
	h.reject(ctx, w, http.StatusTooManyRequests, "rate_limited",
		"limite de requisições do lojista "+merchantID+" excedido", nil)
	return false
}

func (h *WebhookHandler) reject(ctx context.Context, w http.ResponseWriter, status int, outcome, detail string, err error) {
	h.count(ctx, outcome)
	h.logger.WarnContext(ctx, "webhook rejected", slog.String("outcome", outcome), slog.Any("error", err))
	writeProblem(w, status, outcome, detail)
}

func (h *WebhookHandler) count(ctx context.Context, outcome string) {
	h.requests.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
}
