package postgres

import (
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestPoolConfigReadsPasswordOnEachNewConnection(t *testing.T) {
	t.Parallel()
	current := "senha-1"
	cfg, err := poolConfig("postgres://payments@db:5432/payments", func() string { return current })
	if err != nil {
		t.Fatal(err)
	}
	cc := &pgx.ConnConfig{}
	for _, want := range []string{"senha-1", "senha-2"} {
		current = want
		if err := cfg.BeforeConnect(t.Context(), cc); err != nil || cc.Password != want {
			t.Fatalf("password = %q, %v, want %q", cc.Password, err, want)
		}
	}
	if cfg.MaxConnLifetime != MaxConnLifetime {
		t.Fatalf("MaxConnLifetime = %v: conexões antigas nunca seriam recicladas", cfg.MaxConnLifetime)
	}
}

func TestPoolConfigWithoutPasswordFuncKeepsDSN(t *testing.T) {
	t.Parallel()
	cfg, err := poolConfig("postgres://payments:fixa@db:5432/payments", nil)
	if err != nil || cfg.BeforeConnect != nil || cfg.ConnConfig.Password != "fixa" {
		t.Fatalf("cfg = %+v, %v", cfg, err)
	}
}
