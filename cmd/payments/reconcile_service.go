package main

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/csvstatement"
	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/objectstore"
	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/postgres"
	"github.com/JohnnyMatteus/payments-events-go/internal/app"
	"github.com/JohnnyMatteus/payments-events-go/internal/platform/config"
)

// newReconcileService monta o caso de uso de conciliação. Com RECONCILE_MODE=sql
// a comparação acontece no banco (ADR 0022); o padrão compara em memória.
func newReconcileService(cfg config.Config, opener *objectstore.Opener, pool *pgxpool.Pool) *app.ReconcileService {
	svc := app.NewReconcileService(csvstatement.NewReader(opener), postgres.NewLedgerReader(pool))
	if cfg.ReconcileMode == "sql" {
		svc.WithComparer(postgres.NewSQLComparer(pool, cfg.ReconcileIncludeMatches))
	}
	return svc
}
