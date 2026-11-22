package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// ObjectEvent é a notificação de que um objeto foi criado no bucket.
type ObjectEvent struct {
	Bucket    string
	Key       string
	VersionID string
}

// ObjectNotification é uma mensagem da fila: pode trazer vários objetos.
type ObjectNotification struct {
	Receipt string
	Events  []ObjectEvent
}

// ObjectNotifier é a porta da fila de notificações de objetos (S3 → SQS).
// Uma notificação não confirmada volta a ser entregue.
type ObjectNotifier interface {
	Receive(ctx context.Context) ([]ObjectNotification, error)
	Ack(ctx context.Context, n ObjectNotification) error
}

// ObjectClaims garante que cada versão de objeto seja conciliada uma vez.
type ObjectClaims interface {
	// Claim devolve false se a versão já foi reservada antes.
	Claim(ctx context.Context, bucket, key, versionID string) (bool, error)
	// Release desfaz a reserva para que uma nova entrega tente de novo.
	Release(ctx context.Context, bucket, key, versionID string) error
}

// StatementRunner executa a conciliação de um dia.
type StatementRunner interface {
	Run(ctx context.Context, date time.Time, trigger RunTrigger, operator string) (RunSummary, error)
}

// StatementWatcher dispara a conciliação quando um extrato chega ao bucket.
type StatementWatcher struct {
	notifier ObjectNotifier
	claims   ObjectClaims
	runner   StatementRunner
	logger   *slog.Logger
	// idle é a espera após uma falha ao ler a fila, para não girar em falso.
	idle time.Duration
}

// NewStatementWatcher cria o consumidor das notificações.
func NewStatementWatcher(n ObjectNotifier, c ObjectClaims, r StatementRunner, logger *slog.Logger) *StatementWatcher {
	return &StatementWatcher{notifier: n, claims: c, runner: r, logger: logger, idle: 2 * time.Second}
}

// Run consome a fila até o contexto terminar.
func (w *StatementWatcher) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		batch, err := w.notifier.Receive(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			w.logger.ErrorContext(ctx, "receive statement notifications failed", slog.Any("error", err))
			select {
			case <-ctx.Done():
			case <-time.After(w.idle):
			}
			continue
		}
		for _, n := range batch {
			w.process(ctx, n)
		}
	}
	return nil
}

func (w *StatementWatcher) process(ctx context.Context, n ObjectNotification) {
	for _, ev := range n.Events {
		if err := w.Handle(ctx, ev); err != nil {
			// Sem Ack: a fila reentrega e, esgotadas as tentativas, move para a DLQ.
			w.logger.ErrorContext(ctx, "statement reconciliation failed",
				slog.String("key", ev.Key), slog.Any("error", err))
			return
		}
	}
	if err := w.notifier.Ack(ctx, n); err != nil {
		w.logger.ErrorContext(ctx, "ack statement notification failed", slog.Any("error", err))
	}
}

// Handle concilia o dia do extrato notificado, uma vez por versão do objeto.
// Chaves fora de settlements/AAAA-MM-DD.csv são ignoradas.
func (w *StatementWatcher) Handle(ctx context.Context, ev ObjectEvent) error {
	date, ok := SettlementDateFromKey(ev.Key)
	if !ok {
		w.logger.InfoContext(ctx, "object ignored: not a settlement statement", slog.String("key", ev.Key))
		return nil
	}
	claimed, err := w.claims.Claim(ctx, ev.Bucket, ev.Key, ev.VersionID)
	if err != nil {
		return fmt.Errorf("claim %s: %w", ev.Key, err)
	}
	if !claimed {
		w.logger.InfoContext(ctx, "object already reconciled", slog.String("key", ev.Key))
		return nil
	}
	if _, err := w.runner.Run(ctx, date, TriggerS3Notification, "s3"); err != nil {
		if relErr := w.claims.Release(ctx, ev.Bucket, ev.Key, ev.VersionID); relErr != nil {
			err = errors.Join(err, relErr)
		}
		return fmt.Errorf("reconcile %s: %w", ev.Key, err)
	}
	return nil
}
