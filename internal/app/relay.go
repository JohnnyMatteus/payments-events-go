package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// RelayObserver recebe notificações do relay para métricas. Mantém o caso de
// uso livre de bibliotecas de observabilidade.
type RelayObserver interface {
	Published(lag time.Duration)
	Retried(attempt int)
	DeadLettered()
}

// RelayConfig controla o ritmo e a política de retry do relay.
type RelayConfig struct {
	BatchSize    int
	PollInterval time.Duration
	MaxAttempts  int
	Backoff      Backoff
}

// Relay lê mensagens pendentes do outbox e as publica no broker. Falhas de
// publicação são reagendadas com backoff exponencial; após MaxAttempts a
// mensagem é marcada como morta e fica no outbox para análise manual.
type Relay struct {
	cfg       RelayConfig
	outbox    OutboxRepository
	publisher Publisher
	observer  RelayObserver
	logger    *slog.Logger
	now       func() time.Time
}

// NewRelay cria o relay do outbox.
func NewRelay(cfg RelayConfig, outbox OutboxRepository, publisher Publisher,
	observer RelayObserver, logger *slog.Logger) (*Relay, error) {
	if cfg.BatchSize < 1 || cfg.MaxAttempts < 1 || cfg.PollInterval <= 0 || cfg.Backoff.Base <= 0 {
		return nil, errors.New("relay: batch size, max attempts, poll interval and backoff base must be positive")
	}
	return &Relay{
		cfg: cfg, outbox: outbox, publisher: publisher,
		observer: observer, logger: logger, now: time.Now,
	}, nil
}

// Run processa lotes até o contexto ser cancelado. Quando um lote vem cheio,
// o próximo é processado imediatamente para drenar o acúmulo.
func (r *Relay) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.cfg.PollInterval)
	defer ticker.Stop()
	for {
		n, err := r.RunOnce(ctx)
		if err != nil && ctx.Err() == nil {
			r.logger.ErrorContext(ctx, "outbox batch failed", slog.Any("error", err))
		}
		if err == nil && n == r.cfg.BatchSize {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunOnce processa um lote e devolve quantas mensagens foram tratadas.
func (r *Relay) RunOnce(ctx context.Context) (int, error) {
	n, err := r.outbox.ProcessDue(ctx, r.now(), r.cfg.BatchSize, r.deliver)
	if err != nil {
		return n, fmt.Errorf("process outbox: %w", err)
	}
	return n, nil
}

func (r *Relay) deliver(ctx context.Context, msg OutboxMessage) Delivery {
	err := r.publisher.Publish(ctx, msg)
	now := r.now()
	if err == nil {
		r.observer.Published(now.Sub(msg.CreatedAt))
		return Delivery{Status: DeliveryPublished}
	}

	attempt := msg.Attempts + 1
	if attempt >= r.cfg.MaxAttempts {
		r.observer.DeadLettered()
		r.logger.ErrorContext(ctx, "outbox message exhausted retries",
			slog.String("message_id", msg.MessageID), slog.Int("attempts", attempt), slog.Any("error", err))
		return Delivery{Status: DeliveryDead, Err: err}
	}

	next := now.Add(r.cfg.Backoff.Delay(attempt))
	r.observer.Retried(attempt)
	r.logger.WarnContext(ctx, "outbox publish failed, rescheduling",
		slog.String("message_id", msg.MessageID), slog.Int("attempt", attempt),
		slog.Time("next_attempt_at", next), slog.Any("error", err))
	return Delivery{Status: DeliveryRetry, NextAttemptAt: next, Err: err}
}
