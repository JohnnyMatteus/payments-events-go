package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
)

// BalanceProjector é o consumidor de exemplo: mantém o saldo capturado e
// estornado por pagamento a partir de LedgerEntryRecorded. É idempotente
// (cada message_id é aplicado uma vez) e tolerante a ordem (só soma valores).
type BalanceProjector struct {
	name   string
	repo   ProjectionRepository
	logger *slog.Logger
}

// NewBalanceProjector cria o projetor. name identifica o consumidor na
// tabela de mensagens já consumidas.
func NewBalanceProjector(name string, repo ProjectionRepository, logger *slog.Logger) *BalanceProjector {
	return &BalanceProjector{name: name, repo: repo, logger: logger}
}

// Handle aplica uma mensagem. Erros que embrulham ErrPermanent não devem ser
// tentados de novo; os demais são transitórios.
func (p *BalanceProjector) Handle(ctx context.Context, messageID string, body []byte) error {
	if err := decodeLedgerMessage(messageID, body); err != nil {
		return err
	}
	var msg LedgerEntryRecorded
	_ = json.Unmarshal(body, &msg)
	applied, err := p.repo.ApplyOnce(ctx, p.name, messageID, msg)
	if err != nil {
		return fmt.Errorf("apply message %s: %w", messageID, err)
	}
	if !applied {
		p.logger.InfoContext(ctx, "duplicate message skipped", slog.String("message_id", messageID))
	}
	return nil
}

// decodeLedgerMessage valida o contrato da mensagem; erros embrulham
// ErrPermanent.
func decodeLedgerMessage(messageID string, body []byte) error {
	if messageID == "" {
		return fmt.Errorf("%w: message without message_id", ErrPermanent)
	}
	var msg LedgerEntryRecorded
	if err := json.Unmarshal(body, &msg); err != nil {
		return fmt.Errorf("%w: decode message %s: %w", ErrPermanent, messageID, err)
	}
	if err := msg.Validate(); err != nil {
		return fmt.Errorf("%w: message %s: %w", ErrPermanent, messageID, err)
	}
	return nil
}

// IsPermanent informa se o erro não deve ser tentado de novo.
func IsPermanent(err error) bool { return errors.Is(err, ErrPermanent) }
