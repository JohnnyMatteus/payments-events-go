package domain

import (
	"errors"
	"fmt"
	"strings"
)

// Erros das regras de casos de divergência e ajustes.
var (
	ErrCaseNotOpen      = errors.New("case is not open")
	ErrNoAdjustment     = errors.New("divergence cannot be fixed by an adjustment")
	ErrSameOperator     = errors.New("the operator who proposed an adjustment cannot approve it")
	ErrReasonRequired   = errors.New("a reason is required")
	ErrAdjustmentClosed = errors.New("adjustment was already decided")
)

// CaseStatus é o estado de um caso de divergência entre execuções da
// conciliação.
type CaseStatus string

// Estados de um caso. explained e resolved encerram o caso.
const (
	// CaseOpen: divergência apontada e ainda sem tratamento.
	CaseOpen CaseStatus = "open"
	// CaseAdjustmentProposed: há um ajuste aguardando a aprovação de outra pessoa.
	CaseAdjustmentProposed CaseStatus = "adjustment_proposed"
	// CaseExplained: a divergência é legítima e foi justificada (ex.: o
	// provedor liquida no dia seguinte); nada muda no ledger.
	CaseExplained CaseStatus = "explained"
	// CaseResolved: um ajuste aprovado corrigiu o ledger ou uma nova
	// conciliação não encontrou mais a divergência.
	CaseResolved CaseStatus = "resolved"
)

// Closed informa se o caso não pede mais ação.
func (s CaseStatus) Closed() bool { return s == CaseExplained || s == CaseResolved }

// AdjustmentStatus é o estado de um lançamento de ajuste.
type AdjustmentStatus string

// Estados de um ajuste.
const (
	AdjustmentProposed AdjustmentStatus = "proposed"
	AdjustmentApproved AdjustmentStatus = "approved"
	AdjustmentRejected AdjustmentStatus = "rejected"
)

// RequireReason normaliza o motivo de uma decisão humana, que é obrigatório.
func RequireReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "", ErrReasonRequired
	}
	return reason, nil
}

// CanExplain verifica se um caso aceita justificativa.
func CanExplain(status CaseStatus) error {
	if status != CaseOpen {
		return fmt.Errorf("%w: status %s", ErrCaseNotOpen, status)
	}
	return nil
}

// CanApprove aplica a regra dos quatro olhos: quem propõe não aprova.
func CanApprove(status AdjustmentStatus, proposedBy, approver string) error {
	if status != AdjustmentProposed {
		return fmt.Errorf("%w: status %s", ErrAdjustmentClosed, status)
	}
	if strings.EqualFold(proposedBy, approver) {
		return ErrSameOperator
	}
	return nil
}

// AdjustmentFor deriva o lançamento que faz o ledger bater com o extrato
// nesta divergência. ref identifica o ajuste e compõe o transaction_id do
// lançamento quando ele corrige uma transação existente. O ledger continua
// append-only: a correção é sempre um lançamento novo.
func AdjustmentFor(d Divergence, ref string) (LedgerEntry, error) {
	switch d.Kind {
	case MissingInLedger:
		if d.Statement == nil {
			return LedgerEntry{}, fmt.Errorf("%w: statement line is missing", ErrNoAdjustment)
		}
		s := d.Statement
		return LedgerEntry{
			TransactionID: s.TransactionID, PaymentID: s.PaymentID, Direction: s.Direction,
			AmountCents: s.AmountCents, Currency: s.Currency, OccurredAt: s.OccurredAt,
		}, nil
	case AmountMismatch, MissingInStatement:
		if d.Ledger == nil {
			return LedgerEntry{}, fmt.Errorf("%w: ledger entry is missing", ErrNoAdjustment)
		}
		if d.Statement != nil && d.Statement.Currency != d.Ledger.Currency {
			return LedgerEntry{}, fmt.Errorf("%w: currencies differ", ErrNoAdjustment)
		}
		delta := d.DeltaCents()
		if delta == 0 {
			return LedgerEntry{}, fmt.Errorf("%w: delta is zero", ErrNoAdjustment)
		}
		dir := Credit
		if delta < 0 {
			dir, delta = Debit, -delta
		}
		l := d.Ledger
		return LedgerEntry{
			MerchantID: l.MerchantID, TransactionID: l.TransactionID + ":adj:" + ref, PaymentID: l.PaymentID,
			Direction: dir, AmountCents: delta, Currency: l.Currency, OccurredAt: l.OccurredAt,
			AdjustsTransactionID: l.TransactionID,
		}, nil
	default:
		return LedgerEntry{}, fmt.Errorf("%w: kind %s", ErrNoAdjustment, d.Kind)
	}
}
