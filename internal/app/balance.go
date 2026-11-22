package app

import "context"

// BalanceReader é o lado de leitura de saldos.
type BalanceReader interface {
	PaymentBalance(ctx context.Context, paymentID string) (PaymentBalance, error)
	MerchantBalances(ctx context.Context) ([]MerchantBalance, error)
	PaymentBalances(ctx context.Context, merchantID, cursor string, limit int) (Page[PaymentBalance], error)
}

// BalanceService é a consulta de saldo para outros serviços. Qualquer papel
// conhecido consulta; sem ator autenticado nada é lido (falha fechado).
type BalanceService struct {
	reader BalanceReader
}

// NewBalanceService cria o serviço.
func NewBalanceService(reader BalanceReader) *BalanceService {
	return &BalanceService{reader: reader}
}

// PaymentBalance devolve o saldo de um pagamento; ErrNotFound se não há projeção.
func (s *BalanceService) PaymentBalance(ctx context.Context, paymentID string) (PaymentBalance, error) {
	if _, err := Authorize(ctx, RoleReader); err != nil {
		return PaymentBalance{}, err
	}
	if paymentID == "" {
		return PaymentBalance{}, ErrInvalidInput
	}
	return s.reader.PaymentBalance(ctx, paymentID)
}

// MerchantBalances devolve o saldo por lojista.
func (s *BalanceService) MerchantBalances(ctx context.Context) ([]MerchantBalance, error) {
	if _, err := Authorize(ctx, RoleReader); err != nil {
		return nil, err
	}
	return s.reader.MerchantBalances(ctx)
}

// PaymentBalances lista os saldos por pagamento com cursor.
func (s *BalanceService) PaymentBalances(ctx context.Context, merchantID, cursor string, limit int) (Page[PaymentBalance], error) {
	if _, err := Authorize(ctx, RoleReader); err != nil {
		return Page[PaymentBalance]{}, err
	}
	if limit < 0 {
		return Page[PaymentBalance]{}, ErrInvalidInput
	}
	return s.reader.PaymentBalances(ctx, merchantID, cursor, limit)
}
