package main

import (
	"context"
	"log/slog"

	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/httpapi"
	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/postgres"
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
	ingest := app.NewIngestService(postgres.NewEventRecorder(pool))
	webhook, err := httpapi.NewWebhookHandler(verifier, ingest, logger)
	if err != nil {
		return err
	}
	router := httpapi.NewRouter(webhook, tel.MetricsHandler, pool.Ping)
	return serve(ctx, newServer(cfg.HTTPAddr, router), cfg.ShutdownTimeout, logger)
}
