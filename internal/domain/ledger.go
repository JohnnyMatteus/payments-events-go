package domain

import "time"

// Direction é o sentido de um lançamento no ledger do lojista.
type Direction string

// Direções possíveis de um lançamento.
const (
	Credit Direction = "credit"
	Debit  Direction = "debit"
)

// LedgerEntry é um lançamento imutável no ledger. Correções são feitas com
// novos lançamentos, nunca com UPDATE.
type LedgerEntry struct {
	EventID       string
	MerchantID    string
	TransactionID string
	PaymentID     string
	Direction     Direction
	AmountCents   int64
	Currency      Currency
	OccurredAt    time.Time
	// AdjustsTransactionID aponta a transação corrigida por um lançamento de
	// ajuste; vazio nos lançamentos que vieram do provedor.
	AdjustsTransactionID string
}

// NewLedgerEntry deriva o lançamento de um evento já validado. O segundo
// retorno é false quando o tipo de evento não tem efeito financeiro.
func NewLedgerEntry(e Event) (LedgerEntry, bool) {
	dir, ok := e.Direction()
	if !ok {
		return LedgerEntry{}, false
	}
	return LedgerEntry{
		EventID:       e.ID,
		MerchantID:    e.MerchantID,
		TransactionID: e.TransactionID,
		PaymentID:     e.PaymentID,
		Direction:     dir,
		AmountCents:   e.AmountCents,
		Currency:      e.Currency,
		OccurredAt:    e.OccurredAt.UTC(),
	}, true
}

// SignedCents devolve o valor com sinal: positivo para crédito, negativo para débito.
func (l LedgerEntry) SignedCents() int64 {
	if l.Direction == Debit {
		return -l.AmountCents
	}
	return l.AmountCents
}
