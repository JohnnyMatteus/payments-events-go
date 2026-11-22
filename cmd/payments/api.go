package main

import (
	"context"
	"log/slog"

	"golang.org/x/sync/errgroup"

	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/httpapi"
	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/postgres"
	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/redisstore"
	"github.com/JohnnyMatteus/payments-events-go/internal/app"
	"github.com/JohnnyMatteus/payments-events-go/internal/platform/config"
)

func runAPI(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	if err := cfg.Require("DATABASE_URL"); err != nil {
		return err
	}
	tel, shutdown, err := setupTelemetry(ctx, cfg, "payments-api", logger)
	if err != nil {
		return err
	}
	defer shutdown()

	pool, err := openPool(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer pool.Close()

	verifier, err := newVerifier(ctx, cfg, logger)
	if err != nil {
		return err
	}
	recorder := postgres.NewEventRecorder(pool)
	var ingest app.Ingester = app.NewIngestService(recorder)
	var opts []httpapi.WebhookOption
	// Sem REDIS_URL o serviço funciona igual, só sem o atalho de
	// idempotência e sem limite por lojista (o Postgres segue como garantia).
	if cfg.RedisURL != "" {
		client, err := redisstore.Connect(ctx, cfg.RedisURL)
		if err != nil {
			return err
		}
		defer func() { _ = client.Close() }()
		cache, err := redisstore.NewIdempotencyCache(client, cfg.IdempotencyClaimTTL, cfg.IdempotencyDoneTTL)
		if err != nil {
			return err
		}
		ingest = app.NewFastPathIngest(ingest, cache, logger).WithRedeliveryCounter(recorder)
		opts = append(opts, httpapi.WithRateLimiter(
			redisstore.NewFixedWindowLimiter(client, cfg.WebhookRateLimit, cfg.WebhookRateWindow)))
	}
	webhook, err := httpapi.NewWebhookHandler(verifier, ingest, logger, opts...)
	if err != nil {
		return err
	}
	var routerOpts []httpapi.RouterOption
	auth, err := operatorAuthenticator(ctx, cfg, logger)
	if err != nil {
		return err
	}
	admin, err := adminOption(ctx, cfg, pool, auth, logger)
	if err != nil {
		return err
	}
	if admin != nil {
		routerOpts = append(routerOpts, admin)
	}
	router := httpapi.NewRouter(webhook, tel.MetricsHandler, pool.Ping, routerOpts...)
	grpcSrv, err := balanceGRPC(cfg, pool, auth, logger)
	if err != nil {
		return err
	}
	if grpcSrv == nil {
		return serve(ctx, newServer(cfg.HTTPAddr, router), cfg.ShutdownTimeout, logger)
	}
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return serve(gctx, newServer(cfg.HTTPAddr, router), cfg.ShutdownTimeout, logger) })
	g.Go(func() error { return serveGRPC(gctx, grpcSrv, cfg.GRPCAddr, cfg.ShutdownTimeout, logger) })
	return g.Wait()
}
