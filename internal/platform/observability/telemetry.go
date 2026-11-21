// Package observability configura logs estruturados (slog), métricas
// (OpenTelemetry exportadas em formato Prometheus) e traces (OTLP).
package observability

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Telemetry agrupa o handler de /metrics e o desligamento ordenado dos providers.
type Telemetry struct {
	MetricsHandler http.Handler
	shutdown       []func(context.Context) error
}

// Shutdown exporta o que estiver em buffer e encerra os providers.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	var errs []error
	for _, fn := range t.shutdown {
		errs = append(errs, fn(ctx))
	}
	return errors.Join(errs...)
}

// Setup registra os providers globais do OpenTelemetry.
//
// Métricas: sempre ligadas, servidas em /metrics pelo exportador Prometheus,
// junto com métricas de runtime do Go e do processo.
// Traces: exportados via OTLP/HTTP quando OTEL_EXPORTER_OTLP_ENDPOINT (ou
// OTEL_EXPORTER_OTLP_TRACES_ENDPOINT) está definido. Sem exportador, os spans
// continuam sendo criados e o trace_id aparece nos logs.
func Setup(ctx context.Context, serviceName, version string) (*Telemetry, error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(
			attribute.String("service.name", serviceName),
			attribute.String("service.version", version),
		),
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
	)
	if err != nil {
		return nil, fmt.Errorf("build resource: %w", err)
	}

	registry := prometheus.NewRegistry()
	registry.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	exporter, err := otelprom.New(otelprom.WithRegisterer(registry))
	if err != nil {
		return nil, fmt.Errorf("create prometheus exporter: %w", err)
	}
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithResource(res), sdkmetric.WithReader(exporter))
	otel.SetMeterProvider(meterProvider)

	traceOpts := []sdktrace.TracerProviderOption{sdktrace.WithResource(res)}
	if otlpConfigured() {
		traceExporter, err := otlptracehttp.New(ctx)
		if err != nil {
			return nil, fmt.Errorf("create otlp trace exporter: %w", err)
		}
		traceOpts = append(traceOpts, sdktrace.WithBatcher(traceExporter))
	}
	tracerProvider := sdktrace.NewTracerProvider(traceOpts...)
	otel.SetTracerProvider(tracerProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	return &Telemetry{
		MetricsHandler: promhttp.HandlerFor(registry, promhttp.HandlerOpts{}),
		shutdown:       []func(context.Context) error{tracerProvider.Shutdown, meterProvider.Shutdown},
	}, nil
}

func otlpConfigured() bool {
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" || os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") != ""
}
