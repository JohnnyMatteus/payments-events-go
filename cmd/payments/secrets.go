package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/httpapi"
	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/postgres"
	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/secretstore"
	"github.com/JohnnyMatteus/payments-events-go/internal/app"
	"github.com/JohnnyMatteus/payments-events-go/internal/platform/config"
)

// newSecretStore devolve o cofre configurado, ou nil quando os segredos vêm
// das variáveis de ambiente (SECRETS_PROVIDER=env).
func newSecretStore(ctx context.Context, cfg config.Config) (app.SecretStore, error) {
	switch cfg.SecretsProvider {
	case "vault":
		return secretstore.NewVault(ctx, secretstore.VaultConfig{
			Addr: cfg.VaultAddr, Mount: cfg.VaultKVMount, Token: cfg.VaultToken, Role: cfg.VaultK8sRole,
		})
	case "awssm":
		return secretstore.NewAWSSecretsManager(ctx, secretstore.AWSConfig{Endpoint: cfg.SecretsEndpoint, Region: cfg.AWSRegion})
	default:
		return nil, nil //nolint:nilnil // nil significa "sem cofre": os segredos vêm do ambiente
	}
}

// openPool abre o pool do Postgres. Com DATABASE_PASSWORD_REF a senha vem do
// cofre e é relida a cada intervalo, então uma rotação alcança as conexões
// novas sem reiniciar o processo.
func openPool(ctx context.Context, cfg config.Config, logger *slog.Logger) (*pgxpool.Pool, error) {
	if cfg.DatabasePasswordRef == "" {
		return postgres.Connect(ctx, cfg.DatabaseURL)
	}
	store, err := newSecretStore(ctx, cfg)
	if err != nil {
		return nil, err
	}
	var password atomic.Pointer[string]
	rotation := app.NewSecretRotation(store, cfg.DatabasePasswordRef, func(v string) error {
		password.Store(&v)
		return nil
	}, logger)
	if err := rotation.Load(ctx); err != nil {
		return nil, err
	}
	pool, err := postgres.ConnectRotating(ctx, cfg.DatabaseURL, func() string { return *password.Load() })
	if err != nil {
		return nil, err
	}
	go rotation.Watch(ctx, cfg.SecretsRefreshInterval)
	return pool, nil
}

// newVerifier monta o verificador do webhook. Com WEBHOOK_SECRETS_REF os
// segredos ("atual,anterior") vêm do cofre e a rotação vale sem reinício: os
// dois são aceitos até o anterior sair da lista.
func newVerifier(ctx context.Context, cfg config.Config, logger *slog.Logger) (*httpapi.Verifier, error) {
	if cfg.WebhookSecretsRef == "" {
		if len(cfg.WebhookSecrets) == 0 {
			return nil, fmt.Errorf("missing required environment variables: WEBHOOK_SECRETS")
		}
		return httpapi.NewVerifier(cfg.WebhookSecrets, cfg.SignatureTolerance)
	}
	store, err := newSecretStore(ctx, cfg)
	if err != nil {
		return nil, err
	}
	raw, err := store.Get(ctx, cfg.WebhookSecretsRef)
	if err != nil {
		return nil, fmt.Errorf("read webhook secrets: %w", err)
	}
	verifier, err := httpapi.NewVerifier(httpapi.ParseSecretList(raw), cfg.SignatureTolerance)
	if err != nil {
		return nil, err
	}
	rotation := app.NewSecretRotation(store, cfg.WebhookSecretsRef, func(v string) error {
		return verifier.SetSecrets(httpapi.ParseSecretList(v))
	}, logger)
	if err := rotation.Load(ctx); err != nil {
		return nil, err
	}
	go rotation.Watch(ctx, cfg.SecretsRefreshInterval)
	return verifier, nil
}
