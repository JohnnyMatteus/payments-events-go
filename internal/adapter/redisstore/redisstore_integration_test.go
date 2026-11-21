//go:build integration

package redisstore_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/redisstore"
	"github.com/JohnnyMatteus/payments-events-go/internal/app"
	"github.com/JohnnyMatteus/payments-events-go/internal/testinfra"
)

var client *redis.Client

func TestMain(m *testing.M) {
	os.Exit(testinfra.Run(m, func(ctx context.Context) (func(), error) {
		ctr, err := tcredis.Run(ctx, testinfra.RedisImage)
		if err != nil {
			return nil, err
		}
		url, err := ctr.ConnectionString(ctx)
		if err != nil {
			return nil, err
		}
		client, err = redisstore.Connect(ctx, url)
		if err != nil {
			return nil, err
		}
		return func() { _ = client.Close(); _ = ctr.Terminate(context.Background()) }, nil
	}))
}

func TestIdempotencyCacheLifecycle(t *testing.T) {
	ctx := context.Background()
	cache, err := redisstore.NewIdempotencyCache(client, time.Minute, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	steps := []struct {
		action string
		want   app.ClaimState
	}{
		{"claim", app.ClaimAcquired},
		{"claim", app.ClaimInFlight},
		{"release", 0},
		{"claim", app.ClaimAcquired},
		{"complete", 0},
		{"claim", app.ClaimDone},
	}
	for i, s := range steps {
		switch s.action {
		case "claim":
			got, err := cache.Claim(ctx, "evt_it_1")
			if err != nil || got != s.want {
				t.Fatalf("passo %d: got (%v, %v), want %v", i, got, err, s.want)
			}
		case "release":
			if err := cache.Release(ctx, "evt_it_1"); err != nil {
				t.Fatal(err)
			}
		case "complete":
			if err := cache.Complete(ctx, "evt_it_1"); err != nil {
				t.Fatal(err)
			}
		}
	}
	ttl := client.TTL(ctx, redisstore.IdempotencyKey("evt_it_1")).Val()
	if ttl <= time.Minute || ttl > time.Hour {
		t.Fatalf("TTL após complete = %s, want (1m, 1h]", ttl)
	}
}

func TestFixedWindowLimiterSharedCount(t *testing.T) {
	ctx := context.Background()
	// Duas instâncias simulam duas réplicas da API dividindo a mesma cota.
	a := redisstore.NewFixedWindowLimiter(client, 3, time.Minute)
	b := redisstore.NewFixedWindowLimiter(client, 3, time.Minute)
	var allowed int
	for i := range 5 {
		l := a
		if i%2 == 1 {
			l = b
		}
		d, err := l.Allow(ctx, "merchant:loja_it")
		if err != nil {
			t.Fatal(err)
		}
		if d.Allowed {
			allowed++
		} else if d.RetryAfter <= 0 || d.RetryAfter > time.Minute {
			t.Fatalf("RetryAfter = %s", d.RetryAfter)
		}
	}
	if allowed != 3 {
		t.Fatalf("allowed = %d, want 3", allowed)
	}
}
