package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// ErrEmptySecret marca um segredo vazio: nunca é aplicado.
var ErrEmptySecret = errors.New("secret is empty")

// SecretRotation mantém um consumidor em dia com um segredo que muda, sem
// reiniciar o processo e sem janela sem credencial válida: a primeira leitura
// precisa dar certo (senão o processo não sobe), e depois, se a leitura ou a
// aplicação falhar, o último valor bom continua valendo.
type SecretRotation struct {
	store  SecretStore
	ref    string
	apply  func(string) error
	logger *slog.Logger
	last   string
}

// NewSecretRotation cria a rotação para a referência ref.
func NewSecretRotation(store SecretStore, ref string, apply func(string) error, logger *slog.Logger) *SecretRotation {
	return &SecretRotation{store: store, ref: ref, apply: apply, logger: logger}
}

// Load lê e aplica o valor atual. Use na subida: falha fechado.
func (r *SecretRotation) Load(ctx context.Context) error {
	return r.refresh(ctx)
}

func (r *SecretRotation) refresh(ctx context.Context) error {
	v, err := r.store.Get(ctx, r.ref)
	if err != nil {
		return fmt.Errorf("read secret %s: %w", r.ref, err)
	}
	if strings.TrimSpace(v) == "" {
		return fmt.Errorf("secret %s: %w", r.ref, ErrEmptySecret)
	}
	if v == r.last {
		return nil
	}
	if err := r.apply(v); err != nil {
		return fmt.Errorf("apply secret %s: %w", r.ref, err)
	}
	if r.last != "" {
		r.logger.InfoContext(ctx, "secret rotated", slog.String("ref", r.ref))
	}
	r.last = v
	return nil
}

// Watch reaplica o segredo a cada intervalo até o contexto acabar. Um erro só
// é registrado: o valor anterior segue em uso.
func (r *SecretRotation) Watch(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := r.refresh(ctx); err != nil {
				r.logger.WarnContext(ctx, "secret refresh failed, keeping the previous value", slog.Any("error", err))
			}
		}
	}
}
