package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ObjectClaims implementa app.ObjectClaims.
type ObjectClaims struct {
	pool *pgxpool.Pool
}

// NewObjectClaims cria o repositório de reservas de objetos.
func NewObjectClaims(pool *pgxpool.Pool) *ObjectClaims {
	return &ObjectClaims{pool: pool}
}

// Claim reserva a versão do objeto; false se já estava reservada.
func (c *ObjectClaims) Claim(ctx context.Context, bucket, key, versionID string) (bool, error) {
	tag, err := c.pool.Exec(ctx, `
		INSERT INTO statement_object_claims (bucket, object_key, version_id) VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING`, bucket, key, versionID)
	if err != nil {
		return false, fmt.Errorf("claim s3://%s/%s: %w", bucket, key, err)
	}
	return tag.RowsAffected() == 1, nil
}

// Release desfaz a reserva.
func (c *ObjectClaims) Release(ctx context.Context, bucket, key, versionID string) error {
	_, err := c.pool.Exec(ctx, `
		DELETE FROM statement_object_claims WHERE bucket = $1 AND object_key = $2 AND version_id = $3`,
		bucket, key, versionID)
	if err != nil {
		return fmt.Errorf("release s3://%s/%s: %w", bucket, key, err)
	}
	return nil
}
