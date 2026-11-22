package config

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestLoadDefaults(t *testing.T) {
	t.Parallel()
	cfg, err := Load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":8080" || cfg.SignatureTolerance != 5*time.Minute || cfg.OutboxMaxAttempts != 10 {
		t.Fatalf("defaults = %+v", cfg)
	}
}

func TestLoadOverridesAndLists(t *testing.T) {
	t.Parallel()
	cfg, err := Load(env(map[string]string{
		"WEBHOOK_SECRETS":     " new , old ,, ",
		"OUTBOX_BATCH_SIZE":   "50",
		"OUTBOX_BACKOFF_BASE": "250ms",
		"S3_ENDPOINT":         "http://localstack:4566",
		"S3_USE_PATH_STYLE":   "true",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cfg.WebhookSecrets, "|") != "new|old" || cfg.OutboxBatchSize != 50 || cfg.OutboxBackoffBase != 250*time.Millisecond ||
		cfg.S3Endpoint != "http://localstack:4566" || !cfg.S3UsePathStyle || cfg.AWSRegion != "us-east-1" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestLoadReportsAllInvalidValues(t *testing.T) {
	t.Parallel()
	_, err := Load(env(map[string]string{
		"OUTBOX_BATCH_SIZE":    "0",
		"OUTBOX_POLL_INTERVAL": "soon",
		"CONSUMER_PREFETCH":    "-1",
		"S3_USE_PATH_STYLE":    "talvez",
	}))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, key := range []string{"OUTBOX_BATCH_SIZE", "OUTBOX_POLL_INTERVAL", "CONSUMER_PREFETCH", "S3_USE_PATH_STYLE"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error does not mention %s: %v", key, err)
		}
	}
}

func TestRequire(t *testing.T) {
	t.Parallel()
	cfg, _ := Load(env(map[string]string{"DATABASE_URL": "postgres://x"}))
	if err := cfg.Require("DATABASE_URL"); err != nil {
		t.Fatal(err)
	}
	err := cfg.Require("DATABASE_URL", "RABBITMQ_URL", "WEBHOOK_SECRETS")
	if err == nil || !strings.Contains(err.Error(), "RABBITMQ_URL, WEBHOOK_SECRETS") {
		t.Fatalf("err = %v", err)
	}
}

// "prod" ou "Production" não podem passar por produção e ligar o token de dev.
func TestLoadRejectsUnknownAppEnv(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"prod", "Production", "PRODUCTION", "staging", "producao"} {
		if _, err := Load(env(map[string]string{"APP_ENV": v})); err == nil || !strings.Contains(err.Error(), "APP_ENV") {
			t.Errorf("APP_ENV=%q: err = %v, want APP_ENV error", v, err)
		}
	}
	for _, v := range []string{"dev", "production", ""} {
		if _, err := Load(env(map[string]string{"APP_ENV": v})); err != nil {
			t.Errorf("APP_ENV=%q: %v", v, err)
		}
	}
}

func TestLoadValidatesSecretsConfiguration(t *testing.T) {
	t.Parallel()
	bad := map[string]map[string]string{
		"provedor desconhecido":      {"SECRETS_PROVIDER": "consul"},
		"ref sem cofre":              {"WEBHOOK_SECRETS_REF": "a#b"},
		"cofre sem ref do webhook":   {"SECRETS_PROVIDER": "vault"},
		"origem dupla":               {"SECRETS_PROVIDER": "awssm", "WEBHOOK_SECRETS_REF": "a", "WEBHOOK_SECRETS": "x"},
		"token do vault em produção": {"SECRETS_PROVIDER": "vault", "WEBHOOK_SECRETS_REF": "a#b", "APP_ENV": "production", "VAULT_TOKEN": "t"},
	}
	for name, m := range bad {
		if _, err := Load(env(m)); err == nil {
			t.Errorf("%s: Load must fail", name)
		}
	}
	good := map[string]string{"SECRETS_PROVIDER": "vault", "WEBHOOK_SECRETS_REF": "a#b", "APP_ENV": "production", "VAULT_K8S_ROLE": "payments"}
	if _, err := Load(env(good)); err != nil {
		t.Errorf("config válida: %v", err)
	}
}
