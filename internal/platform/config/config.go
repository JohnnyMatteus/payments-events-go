// Package config lê a configuração do processo a partir de variáveis de ambiente.
package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Config reúne a configuração de todos os subcomandos.
type Config struct {
	LogLevel        string
	HTTPAddr        string
	OpsAddr         string
	DatabaseURL     string
	RabbitMQURL     string
	ShutdownTimeout time.Duration

	WebhookSecrets     []string
	SignatureTolerance time.Duration

	OutboxBatchSize       int
	OutboxPollInterval    time.Duration
	OutboxMaxAttempts     int
	OutboxBackoffBase     time.Duration
	OutboxBackoffMax      time.Duration
	PublishConfirmTimeout time.Duration

	ConsumerPrefetch    int
	ConsumerRetryBase   time.Duration
	ConsumerRetryMax    time.Duration
	ConsumerDeliveryMax int

	S3Endpoint     string
	AWSRegion      string
	S3UsePathStyle bool

	StatementsBucket   string
	StatementsQueueURL string
	OperatorTokens     []string

	// AppEnv é "dev" ou "production". Em produção o token estático de
	// operador é recusado e só o OIDC autentica (ADR 0013).
	AppEnv           string
	OIDCIssuerURL    string
	OIDCAudience     string
	OIDCRoleReader   string
	OIDCRoleOperator string

	// Cofre de segredos (ADR 0014). Com SecretsProvider "env" (padrão) os
	// segredos vêm das variáveis; com "vault" ou "awssm", das referências.
	SecretsProvider        string
	SecretsRefreshInterval time.Duration
	WebhookSecretsRef      string
	DatabasePasswordRef    string
	VaultAddr              string
	VaultToken             string
	VaultKVMount           string
	VaultK8sRole           string
	SecretsEndpoint        string

	RedisURL            string
	IdempotencyClaimTTL time.Duration
	IdempotencyDoneTTL  time.Duration
	WebhookRateLimit    int
	WebhookRateWindow   time.Duration
}

// Load lê as variáveis com valores padrão seguros para desenvolvimento.
// Todos os erros de formato são devolvidos juntos.
func Load(getenv func(string) string) (Config, error) {
	p := parser{getenv: getenv}
	cfg := Config{
		LogLevel:        p.str("LOG_LEVEL", "info"),
		HTTPAddr:        p.str("HTTP_ADDR", ":8080"),
		OpsAddr:         p.str("OPS_ADDR", ":9090"),
		DatabaseURL:     p.str("DATABASE_URL", ""),
		RabbitMQURL:     p.str("RABBITMQ_URL", ""),
		ShutdownTimeout: p.duration("SHUTDOWN_TIMEOUT", 15*time.Second),

		WebhookSecrets:     p.list("WEBHOOK_SECRETS"),
		SignatureTolerance: p.duration("WEBHOOK_SIGNATURE_TOLERANCE", 5*time.Minute),

		OutboxBatchSize:       p.positiveInt("OUTBOX_BATCH_SIZE", 100),
		OutboxPollInterval:    p.duration("OUTBOX_POLL_INTERVAL", time.Second),
		OutboxMaxAttempts:     p.positiveInt("OUTBOX_MAX_ATTEMPTS", 10),
		OutboxBackoffBase:     p.duration("OUTBOX_BACKOFF_BASE", time.Second),
		OutboxBackoffMax:      p.duration("OUTBOX_BACKOFF_MAX", 5*time.Minute),
		PublishConfirmTimeout: p.duration("PUBLISH_CONFIRM_TIMEOUT", 5*time.Second),

		ConsumerPrefetch:    p.positiveInt("CONSUMER_PREFETCH", 10),
		ConsumerRetryBase:   p.duration("CONSUMER_RETRY_BASE", 500*time.Millisecond),
		ConsumerRetryMax:    p.duration("CONSUMER_RETRY_MAX", 10*time.Second),
		ConsumerDeliveryMax: p.positiveInt("CONSUMER_DELIVERY_LIMIT", 5),

		S3Endpoint:     p.str("S3_ENDPOINT", ""),
		AWSRegion:      p.str("AWS_REGION", "us-east-1"),
		S3UsePathStyle: p.boolean("S3_USE_PATH_STYLE", false),

		StatementsBucket:   p.str("STATEMENTS_BUCKET", "pagafacil-statements"),
		StatementsQueueURL: p.str("STATEMENTS_QUEUE_URL", ""),
		OperatorTokens:     p.list("OPERATOR_TOKENS"),

		AppEnv:           p.str("APP_ENV", "dev"),
		OIDCIssuerURL:    p.str("OIDC_ISSUER_URL", ""),
		OIDCAudience:     p.str("OIDC_AUDIENCE", "edge-gateway"),
		OIDCRoleReader:   p.str("OIDC_ROLE_READER", "payments.read"),
		OIDCRoleOperator: p.str("OIDC_ROLE_OPERATOR", "payments.write"),

		SecretsProvider:        p.str("SECRETS_PROVIDER", "env"),
		SecretsRefreshInterval: p.duration("SECRETS_REFRESH_INTERVAL", time.Minute),
		WebhookSecretsRef:      p.str("WEBHOOK_SECRETS_REF", ""),
		DatabasePasswordRef:    p.str("DATABASE_PASSWORD_REF", ""),
		VaultAddr:              p.str("VAULT_ADDR", ""),
		VaultToken:             p.str("VAULT_TOKEN", ""),
		VaultKVMount:           p.str("VAULT_KV_MOUNT", "secret"),
		VaultK8sRole:           p.str("VAULT_K8S_ROLE", ""),
		SecretsEndpoint:        p.str("SECRETS_ENDPOINT", ""),

		RedisURL:            p.str("REDIS_URL", ""),
		IdempotencyClaimTTL: p.duration("IDEMPOTENCY_CLAIM_TTL", 30*time.Second),
		IdempotencyDoneTTL:  p.duration("IDEMPOTENCY_DONE_TTL", 24*time.Hour),
		WebhookRateLimit:    p.positiveInt("WEBHOOK_RATE_LIMIT", 600),
		WebhookRateWindow:   p.duration("WEBHOOK_RATE_WINDOW", time.Minute),
	}
	if cfg.AppEnv != "dev" && cfg.AppEnv != "production" {
		p.errs = append(p.errs, fmt.Errorf("APP_ENV: expected dev or production, got %q", cfg.AppEnv))
	}
	p.errs = append(p.errs, cfg.validateSecrets()...)
	return cfg, errors.Join(p.errs...)
}

// Require verifica que as variáveis exigidas por um subcomando estão presentes.
func (c Config) Require(names ...string) error {
	values := map[string]bool{
		"DATABASE_URL":    c.DatabaseURL != "",
		"RABBITMQ_URL":    c.RabbitMQURL != "",
		"WEBHOOK_SECRETS": len(c.WebhookSecrets) > 0,
	}
	var missing []string
	for _, n := range names {
		if !values[n] {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}
	return nil
}

type parser struct {
	getenv func(string) string
	errs   []error
}

func (p *parser) str(key, def string) string {
	if v := strings.TrimSpace(p.getenv(key)); v != "" {
		return v
	}
	return def
}

func (p *parser) list(key string) []string {
	var out []string
	for item := range strings.SplitSeq(p.getenv(key), ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func (p *parser) boolean(key string, def bool) bool {
	v := strings.TrimSpace(p.getenv(key))
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		p.errs = append(p.errs, fmt.Errorf("%s: expected true or false, got %q", key, v))
		return def
	}
	return b
}

func (p *parser) duration(key string, def time.Duration) time.Duration {
	v := strings.TrimSpace(p.getenv(key))
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		p.errs = append(p.errs, fmt.Errorf("%s: expected positive duration (e.g. 5s), got %q", key, v))
		return def
	}
	return d
}

func (p *parser) positiveInt(key string, def int) int {
	v := strings.TrimSpace(p.getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		p.errs = append(p.errs, fmt.Errorf("%s: expected positive integer, got %q", key, v))
		return def
	}
	return n
}

// validateSecrets recusa combinações ambíguas ou inseguras de configuração de segredos.
func (c Config) validateSecrets() []error {
	var errs []error
	switch c.SecretsProvider {
	case "env":
		if c.WebhookSecretsRef != "" || c.DatabasePasswordRef != "" {
			errs = append(errs, errors.New("WEBHOOK_SECRETS_REF/DATABASE_PASSWORD_REF exigem SECRETS_PROVIDER vault ou awssm"))
		}
	case "vault", "awssm":
		if c.WebhookSecretsRef == "" {
			errs = append(errs, errors.New("WEBHOOK_SECRETS_REF é obrigatório com SECRETS_PROVIDER="+c.SecretsProvider))
		}
		if len(c.WebhookSecrets) > 0 {
			errs = append(errs, errors.New("WEBHOOK_SECRETS e WEBHOOK_SECRETS_REF definidos juntos: escolha uma origem"))
		}
		if c.SecretsProvider == "vault" && c.VaultToken != "" && c.AppEnv == "production" {
			errs = append(errs, errors.New("VAULT_TOKEN é só para dev: em production use VAULT_K8S_ROLE"))
		}
	default:
		errs = append(errs, fmt.Errorf("SECRETS_PROVIDER: expected env, vault or awssm, got %q", c.SecretsProvider))
	}
	return errs
}
