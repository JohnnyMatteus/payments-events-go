// Package domain contém as regras de negócio puras do processador de eventos
// de pagamento: eventos do provedor, lançamentos de ledger e conciliação.
// Não depende de banco, fila, HTTP nem de bibliotecas de observabilidade.
package domain

import (
	"errors"
	"fmt"
	"time"
)

// ErrInvalidEvent indica um evento que viola o contrato do provedor.
var ErrInvalidEvent = errors.New("invalid event")

// EventType é o tipo de evento enviado pelo provedor no webhook.
type EventType string

// Tipos de evento com efeito no ledger. Qualquer outro tipo é aceito e
// registrado para auditoria, mas não gera lançamento.
const (
	EventPaymentCaptured EventType = "payment.captured"
	EventPaymentRefunded EventType = "payment.refunded"
)

// Event é um evento de pagamento recebido do provedor.
//
// ID é a chave de idempotência: o provedor reenvia o mesmo ID em cada nova
// tentativa de entrega. TransactionID identifica a operação financeira
// (captura ou estorno) e é a chave usada na conciliação.
type Event struct {
	ID            string
	Type          EventType
	TransactionID string
	PaymentID     string
	AmountCents   int64
	Currency      Currency
	OccurredAt    time.Time
}

// Direction informa a direção do lançamento para tipos monetários.
// O segundo retorno é false quando o tipo não afeta o ledger.
func (e Event) Direction() (Direction, bool) {
	switch e.Type {
	case EventPaymentCaptured:
		return Credit, true
	case EventPaymentRefunded:
		return Debit, true
	default:
		return "", false
	}
}

// Validate verifica o contrato mínimo do evento. Campos monetários só são
// exigidos para tipos que geram lançamento.
func (e Event) Validate() error {
	if e.ID == "" {
		return fmt.Errorf("%w: id is required", ErrInvalidEvent)
	}
	if e.Type == "" {
		return fmt.Errorf("%w: type is required", ErrInvalidEvent)
	}
	if _, monetary := e.Direction(); !monetary {
		return nil
	}
	switch {
	case e.TransactionID == "":
		return fmt.Errorf("%w: transaction_id is required", ErrInvalidEvent)
	case e.PaymentID == "":
		return fmt.Errorf("%w: payment_id is required", ErrInvalidEvent)
	case e.AmountCents <= 0:
		return fmt.Errorf("%w: amount must be positive", ErrInvalidEvent)
	case e.OccurredAt.IsZero():
		return fmt.Errorf("%w: occurred_at is required", ErrInvalidEvent)
	}
	if _, err := ParseCurrency(string(e.Currency)); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidEvent, err)
	}
	return nil
}
