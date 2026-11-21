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
	}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cfg.WebhookSecrets, "|") != "new|old" || cfg.OutboxBatchSize != 50 || cfg.OutboxBackoffBase != 250*time.Millisecond {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestLoadReportsAllInvalidValues(t *testing.T) {
	t.Parallel()
	_, err := Load(env(map[string]string{
		"OUTBOX_BATCH_SIZE":    "0",
		"OUTBOX_POLL_INTERVAL": "soon",
		"CONSUMER_PREFETCH":    "-1",
	}))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, key := range []string{"OUTBOX_BATCH_SIZE", "OUTBOX_POLL_INTERVAL", "CONSUMER_PREFETCH"} {
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
