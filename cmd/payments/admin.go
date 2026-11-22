package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/csvstatement"
	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/httpapi"
	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/objectstore"
	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/oidcauth"
	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/postgres"
	"github.com/JohnnyMatteus/payments-events-go/internal/app"
	"github.com/JohnnyMatteus/payments-events-go/internal/platform/config"
)

// operatorAuthenticator escolhe como os operadores se identificam: OIDC
// quando OIDC_ISSUER_URL está definido, token estático só em dev. Sem nenhum
// dos dois devolve nil e a API de operação fica desligada, em vez de aberta.
func operatorAuthenticator(ctx context.Context, cfg config.Config, logger *slog.Logger) (httpapi.Authenticator, error) {
	switch {
	case cfg.OIDCIssuerURL != "" && len(cfg.OperatorTokens) > 0:
		return nil, errors.New("OIDC_ISSUER_URL e OPERATOR_TOKENS definidos juntos: escolha um modo de autenticação")
	case cfg.OIDCIssuerURL != "":
		a, err := oidcauth.New(ctx, oidcauth.Config{
			IssuerURL: cfg.OIDCIssuerURL, Audience: cfg.OIDCAudience,
			ReaderRole: cfg.OIDCRoleReader, OperatorRole: cfg.OIDCRoleOperator,
		})
		if err != nil {
			return nil, err // devolver *Authenticator nulo viraria interface não nula
		}
		return a, nil
	case len(cfg.OperatorTokens) > 0:
		if cfg.AppEnv == "production" {
			return nil, errors.New("OPERATOR_TOKENS é só para dev: em APP_ENV=production configure OIDC_ISSUER_URL")
		}
		a, err := httpapi.NewOperatorAuth(cfg.OperatorTokens)
		if err != nil {
			return nil, err
		}
		return a, nil
	default:
		logger.WarnContext(ctx, "neither OIDC_ISSUER_URL nor OPERATOR_TOKENS set: operator API disabled")
		return nil, nil
	}
}

// adminOption monta a API de operação (desligada sem autenticação configurada).
func adminOption(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, logger *slog.Logger) (httpapi.RouterOption, error) {
	auth, err := operatorAuthenticator(ctx, cfg, logger)
	if err != nil || auth == nil {
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
