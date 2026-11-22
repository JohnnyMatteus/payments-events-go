// Package redisstore implementa sobre Redis o atalho de idempotência e o
// limitador de taxa do webhook (portas app.IdempotencyCache e app.RateLimiter).
package redisstore

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/JohnnyMatteus/payments-events-go/internal/app"
)

const instrumentationName = "github.com/JohnnyMatteus/payments-events-go/internal/adapter/redisstore"

const (
	claimProcessing = "processing"
	claimDone       = "done"
)

// Connect cria o cliente a partir de uma URL redis://[:senha@]host:porta/db
// e verifica a conexão.
func Connect(ctx context.Context, url string) (*redis.Client, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	client := redis.NewClient(opts)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return client, nil
}

// IdempotencyCache guarda o estado de cada event_id em
// "payments:idem:<event_id>": "processing" com TTL curto enquanto o banco
// grava (um processo que morrer no meio não bloqueia o evento por muito
// tempo) e "done" com TTL longo depois de gravado.
type IdempotencyCache struct {
	client   redis.UniversalClient
	claimTTL time.Duration
	doneTTL  time.Duration
	lookups  metric.Int64Counter
}

var _ app.IdempotencyCache = (*IdempotencyCache)(nil)

// NewIdempotencyCache cria o cache e registra idempotency_fastpath_total.
func NewIdempotencyCache(client redis.UniversalClient, claimTTL, doneTTL time.Duration) (*IdempotencyCache, error) {
	lookups, err := otel.Meter(instrumentationName).Int64Counter("idempotency_fastpath_total",
		metric.WithDescription("Consultas ao atalho de idempotência por resultado (acquired, in_flight, done, error)."))
	if err != nil {
		return nil, err
	}
	for _, result := range []string{"acquired", "in_flight", "done", "error"} {
		lookups.Add(context.Background(), 0, metric.WithAttributes(attribute.String("result", result)))
	}
	return &IdempotencyCache{client: client, claimTTL: claimTTL, doneTTL: doneTTL, lookups: lookups}, nil
}

// IdempotencyKey é a chave usada para um event_id.
func IdempotencyKey(eventID string) string { return "payments:idem:" + eventID }

// Claim faz SET NX GET numa ida só: grava "processing" se a chave não
// existir e devolve o valor anterior se existir.
func (c *IdempotencyCache) Claim(ctx context.Context, eventID string) (app.ClaimState, error) {
	prev, err := c.client.SetArgs(ctx, IdempotencyKey(eventID), claimProcessing,
		redis.SetArgs{Mode: "NX", Get: true, TTL: c.claimTTL}).Result()
	state, result := app.ClaimState(0), "error"
	switch {
	case errors.Is(err, redis.Nil):
		state, result, err = app.ClaimAcquired, "acquired", nil
	case err != nil:
		err = fmt.Errorf("claim %s: %w", eventID, err)
	case prev == claimDone:
		state, result = app.ClaimDone, "done"
	default:
		state, result = app.ClaimInFlight, "in_flight"
	}
	c.lookups.Add(ctx, 1, metric.WithAttributes(attribute.String("result", result)))
	return state, err
}

// Complete marca o evento como gravado.
func (c *IdempotencyCache) Complete(ctx context.Context, eventID string) error {
	return c.client.Set(ctx, IdempotencyKey(eventID), claimDone, c.doneTTL).Err()
}

// Release apaga o claim para que a próxima tentativa vá ao banco.
func (c *IdempotencyCache) Release(ctx context.Context, eventID string) error {
	return c.client.Del(ctx, IdempotencyKey(eventID)).Err()
}

// FixedWindowLimiter conta requisições por chave em janelas fixas
// ("payments:ratelimit:<chave>:<início da janela>"). A contagem fica no Redis,
// então vale para todas as réplicas da API.
type FixedWindowLimiter struct {
	client redis.UniversalClient
	limit  int
	window time.Duration
	now    func() time.Time
}

var _ app.RateLimiter = (*FixedWindowLimiter)(nil)

// NewFixedWindowLimiter cria o limitador: limit requisições por window.
func NewFixedWindowLimiter(client redis.UniversalClient, limit int, window time.Duration) *FixedWindowLimiter {
	return &FixedWindowLimiter{client: client, limit: limit, window: window, now: time.Now}
}

// RateLimitKey é a chave do contador de uma chave lógica na janela que contém t.
func RateLimitKey(key string, window time.Duration, t time.Time) string {
	return "payments:ratelimit:" + key + ":" + strconv.FormatInt(t.Truncate(window).Unix(), 10)
}

// Allow incrementa o contador da janela atual. INCR e EXPIRE NX vão na mesma
// transação, então a chave nunca fica sem TTL.
func (l *FixedWindowLimiter) Allow(ctx context.Context, key string) (app.RateDecision, error) {
	now := l.now()
	windowKey := RateLimitKey(key, l.window, now)
	var incr *redis.IntCmd
	if _, err := l.client.TxPipelined(ctx, func(p redis.Pipeliner) error {
		incr = p.Incr(ctx, windowKey)
		p.ExpireNX(ctx, windowKey, l.window)
		return nil
	}); err != nil {
		return app.RateDecision{}, fmt.Errorf("rate limit %s: %w", key, err)
	}
	count := int(incr.Val())
	decision := app.RateDecision{Allowed: count <= l.limit, Limit: l.limit, Remaining: max(l.limit-count, 0)}
	if !decision.Allowed {
		decision.RetryAfter = now.Truncate(l.window).Add(l.window).Sub(now)
	}
	return decision, nil
}
