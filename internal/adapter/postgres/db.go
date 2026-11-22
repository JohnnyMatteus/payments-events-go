package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Connect abre o pool de conexões e verifica que o banco responde.
func Connect(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	return ConnectRotating(ctx, dsn, nil)
}

// ConnectRotating é Connect com a senha lida a cada conexão nova. Com a senha
// vindo de um cofre, uma rotação vale para as próximas conexões sem reiniciar:
// as antigas continuam abertas e são reciclados em MaxConnLifetime, então o
// banco precisa aceitar a senha antiga até esse prazo (duas credenciais).
func ConnectRotating(ctx context.Context, dsn string, password func() string) (*pgxpool.Pool, error) {
	cfg, err := poolConfig(dsn, password)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// MaxConnLifetime limita quanto uma conexão vive, o que faz a rotação de
// senha alcançar todo o pool.
const MaxConnLifetime = 15 * time.Minute

func poolConfig(dsn string, password func() string) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.MaxConnIdleTime = 5 * time.Minute
	if password != nil {
		cfg.MaxConnLifetime = MaxConnLifetime
		cfg.MaxConnLifetimeJitter = time.Minute
		cfg.BeforeConnect = func(_ context.Context, cc *pgx.ConnConfig) error {
			cc.Password = password()
			return nil
		}
	}
	return cfg, nil
}
