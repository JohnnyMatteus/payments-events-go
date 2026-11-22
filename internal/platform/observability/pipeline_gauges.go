package observability

import (
	"context"
	"errors"
	"log/slog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// PipelineSnapshot é o estado do pipeline lido do banco, para os alertas que
// não têm contador: dead letters abertas, divergências sem decisão e conciliação parada.
type PipelineSnapshot struct {
	OutboxPending          int64
	OutboxDead             int64
	DeadLettersOpen        int64
	OpenCases              int64
	OldestPendingSeconds   float64
	LastReconciliationUnix float64 // 0 se nunca houve execução
}

// RegisterPipelineGauges publica o snapshot como gauges, lidos a cada coleta
// do Prometheus. Se a leitura falhar, nada é publicado nessa coleta (a regra
// PipelineGaugesAbsent, em deploy/prometheus/alerts.yml, pega a ausência), em
// vez de repetir um valor velho.
func RegisterPipelineGauges(read func(context.Context) (PipelineSnapshot, error), logger *slog.Logger) error {
	m := otel.Meter(meterName)
	pending, e1 := m.Int64ObservableGauge("pipeline_outbox_pending", metric.WithDescription("Mensagens pendentes no outbox."))
	dead, e2 := m.Int64ObservableGauge("pipeline_outbox_dead", metric.WithDescription("Mensagens mortas no outbox."))
	open, e3 := m.Int64ObservableGauge("pipeline_dead_letters_open", metric.WithDescription("Dead letters abertas (DLQ do broker e outbox esgotado)."))
	cases, e4 := m.Int64ObservableGauge("reconciliation_open_cases", metric.WithDescription("Divergências da conciliação sem decisão."))
	oldest, e5 := m.Float64ObservableGauge("pipeline_oldest_pending_seconds", metric.WithUnit("s"),
		metric.WithDescription("Idade da mensagem pendente mais antiga do outbox."))
	last, e6 := m.Float64ObservableGauge("reconciliation_last_run_timestamp_seconds", metric.WithUnit("s"),
		metric.WithDescription("Quando a última conciliação terminou (0 se nunca rodou)."))
	if err := errors.Join(e1, e2, e3, e4, e5, e6); err != nil {
		return err
	}
	_, err := m.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		s, err := read(ctx)
		if err != nil {
			logger.WarnContext(ctx, "pipeline gauges unavailable", slog.Any("error", err))
			return nil
		}
		o.ObserveInt64(pending, s.OutboxPending)
		o.ObserveInt64(dead, s.OutboxDead)
		o.ObserveInt64(open, s.DeadLettersOpen)
		o.ObserveInt64(cases, s.OpenCases)
		o.ObserveFloat64(oldest, s.OldestPendingSeconds)
		o.ObserveFloat64(last, s.LastReconciliationUnix)
		return nil
	}, pending, dead, open, cases, oldest, last)
	return err
}
