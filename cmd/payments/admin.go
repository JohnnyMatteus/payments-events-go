package main

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/csvstatement"
	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/httpapi"
	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/objectstore"
	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/postgres"
	"github.com/JohnnyMatteus/payments-events-go/internal/app"
	"github.com/JohnnyMatteus/payments-events-go/internal/platform/config"
)

// adminOption monta a API de operação. Sem OPERATOR_TOKENS ela fica desligada
// (nenhuma rota /api/v1 é registrada), em vez de abrir sem autenticação.
func adminOption(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, logger *slog.Logger) (httpapi.RouterOption, error) {
	if len(cfg.OperatorTokens) == 0 {
		logger.WarnContext(ctx, "OPERATOR_TOKENS is empty: operator API disabled")
		return nil, nil
	}
	auth, err := httpapi.NewOperatorAuth(cfg.OperatorTokens)
	if err != nil {
		return nil, err
	}
	opener, err := objectstore.New(ctx, objectstore.S3Config{
		Endpoint: cfg.S3Endpoint, Region: cfg.AWSRegion, UsePathStyle: cfg.S3UsePathStyle,
	})
	if err != nil {
		return nil, err
	}
	statements := opener.Statements(cfg.StatementsBucket)
	runs := postgres.NewReconciliationStore(pool)
	cases := postgres.NewCaseRepository(pool)
	reconcile := app.NewReconcileService(csvstatement.NewReader(opener), postgres.NewLedgerReader(pool))

	admin := httpapi.NewAdminHandler(httpapi.AdminDeps{
		Reader:      postgres.NewOperationsReader(pool),
		DeadLetters: app.NewDeadLetterService(postgres.NewDeadLetterRepository(pool)),
		Statements:  app.NewStatementService(statements, csvstatement.Parse),
		Runner:      app.NewReconciliationRunner(statements, reconcile, runs, logger),
		Runs:        runs,
		Cases:       cases,
		CaseService: app.NewCaseService(cases),
		Logger:      logger,
	})
	return httpapi.WithAdmin(admin, auth), nil
}
