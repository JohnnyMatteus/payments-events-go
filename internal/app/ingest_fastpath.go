package app

import (
	"context"
	"log/slog"

	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
)

// Ingester é o contrato do caso de uso de ingestão, implementado por
// IngestService e pelo decorator FastPathIngest.
type Ingester interface {
	Ingest(ctx context.Context, ev domain.Event, raw []byte) (IngestOutcome, error)
}

// FastPathIngest decora um Ingester com o atalho de idempotência: reentregas
// de eventos já gravados são respondidas sem tocar no banco.
//
// O cache nunca decide sozinho que um evento novo é duplicado. Em erro do
// cache ou com outro processo gravando o mesmo evento (ClaimInFlight), a
// decisão fica com o Postgres; se a gravação falhar, o claim é liberado para
// que a nova tentativa do provedor não seja tomada por duplicata.
type FastPathIngest struct {
	next   Ingester
	cache  IdempotencyCache
	logger *slog.Logger
}

// NewFastPathIngest cria o decorator.
func NewFastPathIngest(next Ingester, cache IdempotencyCache, logger *slog.Logger) *FastPathIngest {
	return &FastPathIngest{next: next, cache: cache, logger: logger}
}

// Ingest implementa Ingester.
func (f *FastPathIngest) Ingest(ctx context.Context, ev domain.Event, raw []byte) (IngestOutcome, error) {
	state, err := f.cache.Claim(ctx, ev.ID)
	if err != nil {
		f.logger.WarnContext(ctx, "idempotency cache unavailable, falling back to database",
			slog.String("event_id", ev.ID), slog.Any("error", err))
		return f.next.Ingest(ctx, ev, raw)
	}
	switch state {
	case ClaimDone:
		return OutcomeDuplicate, nil
	case ClaimInFlight:
		return f.next.Ingest(ctx, ev, raw)
	}

	outcome, err := f.next.Ingest(ctx, ev, raw)
	if err != nil {
		if relErr := f.cache.Release(context.WithoutCancel(ctx), ev.ID); relErr != nil {
			f.logger.WarnContext(ctx, "idempotency claim release failed",
				slog.String("event_id", ev.ID), slog.Any("error", relErr))
		}
		return "", err
	}
	if err := f.cache.Complete(context.WithoutCancel(ctx), ev.ID); err != nil {
		f.logger.WarnContext(ctx, "idempotency claim completion failed",
			slog.String("event_id", ev.ID), slog.Any("error", err))
	}
	return outcome, nil
}
