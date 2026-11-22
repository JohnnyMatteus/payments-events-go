package observability

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

const meterName = "github.com/JohnnyMatteus/payments-events-go/internal/platform/observability"

// RelayMetrics implementa app.RelayObserver com instrumentos do OpenTelemetry.
type RelayMetrics struct {
	published metric.Int64Counter
	retried   metric.Int64Counter
	dead      metric.Int64Counter
	lag       metric.Float64Histogram
}

// NewRelayMetrics registra as métricas do relay no MeterProvider global.
func NewRelayMetrics() (*RelayMetrics, error) {
	m := otel.Meter(meterName)
	published, errP := m.Int64Counter("outbox_published_total",
		metric.WithDescription("Mensagens do outbox confirmadas pelo broker."))
	retried, errR := m.Int64Counter("outbox_retries_total",
		metric.WithDescription("Falhas de publicação reagendadas com backoff."))
	dead, errD := m.Int64Counter("outbox_dead_total",
		metric.WithDescription("Mensagens que esgotaram as tentativas e ficaram como dead no outbox."))
	lag, errL := m.Float64Histogram("outbox_publish_lag_seconds",
		metric.WithDescription("Tempo entre a gravação no outbox e a confirmação do broker."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(0.01, 0.05, 0.1, 0.5, 1, 2, 5, 15, 60, 300))
	if err := errors.Join(errP, errR, errD, errL); err != nil {
		return nil, err
	}
	// Um contador OTel só aparece no /metrics depois do primeiro Add, e o
	// Prometheus não conta o salto de "série ausente" para 1 em increase():
	// sem isto a primeira mensagem morta passaria sem alerta. Add(0) cria a série em 0.
	ctx := context.Background()
	published.Add(ctx, 0)
	retried.Add(ctx, 0)
	dead.Add(ctx, 0)
	return &RelayMetrics{published: published, retried: retried, dead: dead, lag: lag}, nil
}

// Published registra uma publicação confirmada e o atraso desde a gravação.
func (m *RelayMetrics) Published(lag time.Duration) {
	ctx := context.Background()
	m.published.Add(ctx, 1)
	m.lag.Record(ctx, lag.Seconds())
}

// Retried registra uma falha reagendada.
func (m *RelayMetrics) Retried(int) { m.retried.Add(context.Background(), 1) }

// DeadLettered registra uma mensagem que esgotou as tentativas.
func (m *RelayMetrics) DeadLettered() { m.dead.Add(context.Background(), 1) }
