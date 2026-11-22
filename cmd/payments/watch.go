package main

import (
	"context"
	"errors"
	"log/slog"

	"golang.org/x/sync/errgroup"

	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/httpapi"
	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/objectstore"
	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/postgres"
	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/sqsqueue"
	"github.com/JohnnyMatteus/payments-events-go/internal/app"
	"github.com/JohnnyMatteus/payments-events-go/internal/platform/config"
)

// runWatch concilia o dia de cada extrato que o S3 notifica na fila SQS.
func runWatch(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	if err := cfg.Require("DATABASE_URL"); err != nil {
		return err
	}
	if cfg.StatementsQueueURL == "" {
		return errors.New("missing required environment variables: STATEMENTS_QUEUE_URL")
	}
	tel, shutdown, err := setupTelemetry(ctx, cfg, "payments-watch", logger)
	if err != nil {
		return err
	}
	defer shutdown()

	pool, err := openPool(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer pool.Close()

	opener, err := objectstore.New(ctx, objectstore.S3Config{
		Endpoint: cfg.S3Endpoint, Region: cfg.AWSRegion, UsePathStyle: cfg.S3UsePathStyle,
	})
	if err != nil {
		return err
	}
	queue, err := sqsqueue.New(ctx, sqsqueue.Config{
		Endpoint: cfg.S3Endpoint, Region: cfg.AWSRegion, QueueURL: cfg.StatementsQueueURL,
	})
	if err != nil {
		return err
	}
	reconcile := newReconcileService(cfg, opener, pool)
	runner := app.NewReconciliationRunner(opener.Statements(cfg.StatementsBucket), reconcile,
		postgres.NewReconciliationStore(pool), logger)
	watcher := app.NewStatementWatcher(queue, postgres.NewObjectClaims(pool), runner, logger)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		ops := httpapi.NewOpsRouter(tel.MetricsHandler, pool.Ping)
		return serve(gctx, newServer(cfg.OpsAddr, ops), cfg.ShutdownTimeout, logger)
	})
	g.Go(func() error { return watcher.Run(gctx) })
	return g.Wait()
}
