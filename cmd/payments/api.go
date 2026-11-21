package main

import (
	"context"
	"log/slog"

	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/httpapi"
	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/postgres"
	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/redisstore"
	"github.com/JohnnyMatteus/payments-events-go/internal/app"
	"github.com/JohnnyMatteus/payments-events-go/internal/platform/config"
)

func runAPI(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	if err := cfg.Require("DATABASE_URL", "WEBHOOK_SECRETS"); err != nil {
		return err
	}
	tel, shutdown, err := setupTelemetry(ctx, cfg, "payments-api", logger)
	if err != nil {
		return err
	}
	defer shutdown()

	pool, err := postgres.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	verifier, err := httpapi.NewVerifier(cfg.WebhookSecrets, cfg.SignatureTolerance)
	if err != nil {
		return err
	}
	var ingest app.Ingester = app.NewIngestService(postgres.NewEventRecorder(pool))
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
		ingest = app.NewFastPathIngest(ingest, cache, logger)
		opts = append(opts, httpapi.WithRateLimiter(
			redisstore.NewFixedWindowLimiter(client, cfg.WebhookRateLimit, cfg.WebhookRateWindow)))
	}
	webhook, err := httpapi.NewWebhookHandler(verifier, ingest, logger, opts...)
	if err != nil {
		return err
	}
	router := httpapi.NewRouter(webhook, tel.MetricsHandler, pool.Ping)
	return serve(ctx, newServer(cfg.HTTPAddr, router), cfg.ShutdownTimeout, logger)
}
