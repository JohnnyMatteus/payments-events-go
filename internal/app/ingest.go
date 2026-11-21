package app

import (
	"context"
	"fmt"

	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
)

// IngestService é o caso de uso de receber um evento do provedor.
type IngestService struct {
	recorder EventRecorder
}

// NewIngestService cria o caso de uso de ingestão.
func NewIngestService(recorder EventRecorder) *IngestService {
	return &IngestService{recorder: recorder}
}

// Ingest valida o evento, deriva o lançamento e a mensagem de integração e
// delega a gravação atômica ao EventRecorder. Erros de validação embrulham
// domain.ErrInvalidEvent; qualquer outro erro é de infraestrutura e o
// provedor deve tentar de novo.
func (s *IngestService) Ingest(ctx context.Context, ev domain.Event, raw []byte) (IngestOutcome, error) {
	if err := ev.Validate(); err != nil {
		return "", err
	}
	rec := Recording{Event: ev, RawPayload: raw}
	if entry, ok := domain.NewLedgerEntry(ev); ok {
		msg, err := newLedgerEntryRecordedMessage(entry)
		if err != nil {
			return "", err
		}
		rec.Entry = &entry
		rec.Message = &msg
	}
	outcome, err := s.recorder.Record(ctx, rec)
	if err != nil {
		return "", fmt.Errorf("record event %s: %w", ev.ID, err)
	}
	return outcome, nil
}
