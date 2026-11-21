package app

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
)

// RoutingKeyLedgerEntryRecorded é a routing key do evento de integração
// publicado a cada novo lançamento.
const RoutingKeyLedgerEntryRecorded = "ledger.entry_recorded"

// LedgerEntryRecorded é o contrato público (versão 1) do evento de integração
// publicado no broker. Mudanças incompatíveis exigem uma nova versão.
type LedgerEntryRecorded struct {
	SchemaVersion int              `json:"schema_version"`
	EventID       string           `json:"event_id"`
	TransactionID string           `json:"transaction_id"`
	PaymentID     string           `json:"payment_id"`
	Direction     domain.Direction `json:"direction"`
	AmountCents   int64            `json:"amount_cents"`
	Currency      domain.Currency  `json:"currency"`
	OccurredAt    time.Time        `json:"occurred_at"`
}

// Validate verifica o contrato da mensagem recebida pelo consumidor.
func (m LedgerEntryRecorded) Validate() error {
	switch {
	case m.SchemaVersion != 1:
		return fmt.Errorf("unsupported schema_version %d", m.SchemaVersion)
	case m.PaymentID == "" || m.TransactionID == "":
		return fmt.Errorf("payment_id and transaction_id are required")
	case m.Direction != domain.Credit && m.Direction != domain.Debit:
		return fmt.Errorf("invalid direction %q", m.Direction)
	case m.AmountCents <= 0:
		return fmt.Errorf("amount_cents must be positive")
	}
	return nil
}

func newLedgerEntryRecordedMessage(e domain.LedgerEntry) (OutboxMessage, error) {
	payload, err := json.Marshal(LedgerEntryRecorded{
		SchemaVersion: 1,
		EventID:       e.EventID,
		TransactionID: e.TransactionID,
		PaymentID:     e.PaymentID,
		Direction:     e.Direction,
		AmountCents:   e.AmountCents,
		Currency:      e.Currency,
		OccurredAt:    e.OccurredAt,
	})
	if err != nil {
		return OutboxMessage{}, fmt.Errorf("marshal ledger entry recorded: %w", err)
	}
	return OutboxMessage{
		MessageID:  e.EventID,
		RoutingKey: RoutingKeyLedgerEntryRecorded,
		Payload:    payload,
	}, nil
}
