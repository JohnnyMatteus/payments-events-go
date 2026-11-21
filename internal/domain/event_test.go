package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
)

func validCapture() domain.Event {
	return domain.Event{
		ID:            "evt_001",
		Type:          domain.EventPaymentCaptured,
		TransactionID: "txn_001",
		PaymentID:     "pay_001",
		AmountCents:   15000,
		Currency:      "BRL",
		OccurredAt:    time.Date(2026, 11, 20, 13, 0, 0, 0, time.UTC),
	}
}

func TestEventValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		mutate  func(*domain.Event)
		wantErr bool
	}{
		{name: "captura válida", mutate: func(*domain.Event) {}},
		{name: "estorno válido", mutate: func(e *domain.Event) { e.Type = domain.EventPaymentRefunded }},
		{name: "tipo desconhecido dispensa campos monetários", mutate: func(e *domain.Event) {
			*e = domain.Event{ID: "evt_x", Type: "payment.dispute_opened"}
		}},
		{name: "sem id", mutate: func(e *domain.Event) { e.ID = "" }, wantErr: true},
		{name: "sem tipo", mutate: func(e *domain.Event) { e.Type = "" }, wantErr: true},
		{name: "sem transação", mutate: func(e *domain.Event) { e.TransactionID = "" }, wantErr: true},
		{name: "sem pagamento", mutate: func(e *domain.Event) { e.PaymentID = "" }, wantErr: true},
		{name: "valor zero", mutate: func(e *domain.Event) { e.AmountCents = 0 }, wantErr: true},
		{name: "valor negativo", mutate: func(e *domain.Event) { e.AmountCents = -1 }, wantErr: true},
		{name: "moeda inválida", mutate: func(e *domain.Event) { e.Currency = "REAL" }, wantErr: true},
		{name: "sem data", mutate: func(e *domain.Event) { e.OccurredAt = time.Time{} }, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ev := validCapture()
			tt.mutate(&ev)
			err := ev.Validate()
			if tt.wantErr != (err != nil) {
				t.Fatalf("Validate() err = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, domain.ErrInvalidEvent) {
				t.Fatalf("error %v does not wrap ErrInvalidEvent", err)
			}
		})
	}
}

func TestNewLedgerEntry(t *testing.T) {
	t.Parallel()
	capture := validCapture()
	capture.OccurredAt = time.Date(2026, 11, 20, 10, 0, 0, 0, time.FixedZone("BRT", -3*3600))

	entry, ok := domain.NewLedgerEntry(capture)
	if !ok {
		t.Fatal("captura deveria gerar lançamento")
	}
	if entry.Direction != domain.Credit || entry.SignedCents() != 15000 {
		t.Fatalf("captura: direction=%s signed=%d", entry.Direction, entry.SignedCents())
	}
	if entry.OccurredAt.Location() != time.UTC || entry.OccurredAt.Hour() != 13 {
		t.Fatalf("occurred_at deveria ser normalizado para UTC, got %s", entry.OccurredAt)
	}

	refund := validCapture()
	refund.Type = domain.EventPaymentRefunded
	entry, _ = domain.NewLedgerEntry(refund)
	if entry.Direction != domain.Debit || entry.SignedCents() != -15000 {
		t.Fatalf("estorno: direction=%s signed=%d", entry.Direction, entry.SignedCents())
	}

	if _, ok := domain.NewLedgerEntry(domain.Event{ID: "evt_x", Type: "payment.dispute_opened"}); ok {
		t.Fatal("tipo sem efeito financeiro não deveria gerar lançamento")
	}
}
