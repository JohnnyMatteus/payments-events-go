package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/objectstore"
	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/postgres"
	"github.com/JohnnyMatteus/payments-events-go/internal/app"
	"github.com/JohnnyMatteus/payments-events-go/internal/platform/config"
)

// runRetention executa a política de retenção uma vez (para CronJob): limpa o
// outbox publicado, arquiva payloads de eventos antigos e exporta meses
// fechados do ledger. Nada disso apaga ledger nem chave de idempotência.
func runRetention(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	if err := cfg.Require("DATABASE_URL"); err != nil {
		return err
	}
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
	repo := postgres.NewRetention(pool)
	svc, err := app.NewRetentionService(app.RetentionPolicy{
		OutboxPublishedAfter: cfg.RetentionOutboxAfter,
		EventPayloadsAfter:   cfg.RetentionEventsAfter,
		LedgerExportAfter:    cfg.RetentionLedgerExportAfter,
		BatchSize:            cfg.RetentionBatchSize,
	}, repo, repo, opener.Archive(cfg.LedgerArchiveBucket), logger)
	if err != nil {
		return err
	}
	_, err = svc.Run(ctx, time.Now())
	return err
}
