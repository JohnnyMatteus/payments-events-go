package main

import (
	"context"
	"log/slog"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/httpapi"
	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/postgres"
	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/rabbitmq"
	"github.com/JohnnyMatteus/payments-events-go/internal/app"
	"github.com/JohnnyMatteus/payments-events-go/internal/platform/config"
	"github.com/JohnnyMatteus/payments-events-go/internal/platform/observability"
)

func runRelay(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	if err := cfg.Require("DATABASE_URL", "RABBITMQ_URL"); err != nil {
		return err
	}
	tel, shutdown, err := setupTelemetry(ctx, cfg, "payments-relay", logger)
	if err != nil {
		return err
	}
	defer shutdown()

	pool, err := openPool(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer pool.Close()

	topology := rabbitmq.DefaultTopology()
	topology.DeliveryLimit = cfg.ConsumerDeliveryMax
	publisher := rabbitmq.NewPublisher(cfg.RabbitMQURL, topology, cfg.PublishConfirmTimeout)
	defer func() {
		if err := publisher.Close(); err != nil {
			logger.Warn("publisher close failed", slog.Any("error", err))
		}
	}()

	metrics, err := observability.NewRelayMetrics()
	if err != nil {
		return err
	}
	reader := postgres.NewOperationsReader(pool)
	// Gauges de estado (dead letters abertas, divergências, conciliação parada): só o relay as publica,
	// um processo por implantação, para os alertas não dependerem de quantas réplicas da API existem.
	if err := observability.RegisterPipelineGauges(func(ctx context.Context) (observability.PipelineSnapshot, error) {
		h, err := reader.PipelineHealth(ctx, time.Now())
		if err != nil {
			return observability.PipelineSnapshot{}, err
		}
		s := observability.PipelineSnapshot{
			OutboxPending: int64(h.OutboxPending), OutboxDead: int64(h.OutboxDead),
			DeadLettersOpen: int64(h.DeadLettersOpen), OpenCases: int64(h.OpenCases),
			OldestPendingSeconds: h.OldestPendingAge.Seconds(),
		}
		if h.LastReconciliationAt != nil {
			s.LastReconciliationUnix = float64(h.LastReconciliationAt.Unix())
		}
		return s, nil
	}, logger); err != nil {
		return err
	}
	relay, err := app.NewRelay(app.RelayConfig{
		BatchSize:    cfg.OutboxBatchSize,
		PollInterval: cfg.OutboxPollInterval,
		MaxAttempts:  cfg.OutboxMaxAttempts,
		Backoff: app.Backoff{
			Base: cfg.OutboxBackoffBase, Max: cfg.OutboxBackoffMax, Jitter: app.EqualJitter,
		},
		// LISTEN/NOTIFY acorda o relay na hora; o polling segue como rede de segurança.
		Wake: postgres.NewOutboxNotifier(pool).Wake(ctx),
	}, postgres.NewOutboxRepository(pool), publisher, metrics, logger)
	if err != nil {
		return err
	}

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		ops := httpapi.NewOpsRouter(tel.MetricsHandler, pool.Ping)
		return serve(gctx, newServer(cfg.OpsAddr, ops), cfg.ShutdownTimeout, logger)
	})
	g.Go(func() error {
		logger.InfoContext(gctx, "outbox relay started")
		return relay.Run(gctx)
	})
	return g.Wait()
}
