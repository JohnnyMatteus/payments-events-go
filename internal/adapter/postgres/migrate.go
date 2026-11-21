// Package postgres implementa as portas de persistência sobre PostgreSQL (pgx).
package postgres

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrationLockID é a chave do advisory lock que serializa migrações
// concorrentes (várias réplicas subindo ao mesmo tempo).
const migrationLockID = 7_316_420_001

// Migrate aplica, em ordem, as migrações SQL embutidas que ainda não rodaram.
// Cada arquivo roda em sua própria transação. Devolve os arquivos aplicados.
func Migrate(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockID); err != nil {
		return nil, fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() {
		// O lock é de sessão; se o unlock falhar, ele cai junto com a conexão.
		_, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", migrationLockID)
	}()

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    text        PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return nil, fmt.Errorf("create schema_migrations: %w", err)
	}

	files, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	slices.Sort(files)

	var applied []string
	for _, file := range files {
		version := strings.TrimSuffix(strings.TrimPrefix(file, "migrations/"), ".sql")
		ok, err := applyMigration(ctx, conn.Conn(), version, file)
		if err != nil {
			return applied, err
		}
		if ok {
			applied = append(applied, version)
		}
	}
	return applied, nil
}

func applyMigration(ctx context.Context, conn *pgx.Conn, version, file string) (bool, error) {
	var exists bool
	if err := conn.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)", version).Scan(&exists); err != nil {
		return false, fmt.Errorf("check migration %s: %w", version, err)
	}
	if exists {
		return false, nil
	}
	sql, err := migrationsFS.ReadFile(file)
	if err != nil {
		return false, fmt.Errorf("read migration %s: %w", version, err)
	}
	err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", version)
		return err
	})
	if err != nil {
		return false, fmt.Errorf("apply migration %s: %w", version, err)
	}
	return true, nil
}
