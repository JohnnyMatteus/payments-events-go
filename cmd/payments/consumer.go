package main

import (
	"context"
	"log/slog"

	"golang.org/x/sync/errgroup"

	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/httpapi"
	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/postgres"
	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/rabbitmq"
	"github.com/JohnnyMatteus/payments-events-go/internal/app"
	"github.com/JohnnyMatteus/payments-events-go/internal/platform/config"
)

func runConsumer(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	if err := cfg.Require("DATABASE_URL", "RABBITMQ_URL"); err != nil {
		return err
	}
	tel, shutdown, err := setupTelemetry(ctx, cfg, "payments-consumer", logger)
	if err != nil {
		return err
	}
	defer shutdown()

	pool, err := postgres.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	topology := rabbitmq.DefaultTopology()
	topology.DeliveryLimit = cfg.ConsumerDeliveryMax
	consumer, err := rabbitmq.NewConsumer(cfg.RabbitMQURL, topology, rabbitmq.ConsumerConfig{
		Prefetch: cfg.ConsumerPrefetch,
		Backoff:  app.Backoff{Base: cfg.ConsumerRetryBase, Max: cfg.ConsumerRetryMax, Jitter: app.EqualJitter},
	}, logger)
	if err != nil {
		return err
	}
	projector := app.NewBalanceProjector(topology.Queue, postgres.NewProjectionRepository(pool), logger)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		ops := httpapi.NewOpsRouter(tel.MetricsHandler, pool.Ping)
		return serve(gctx, newServer(cfg.OpsAddr, ops), cfg.ShutdownTimeout, logger)
	})
	g.Go(func() error { return consumer.Run(gctx, projector.Handle) })
	return g.Wait()
}
