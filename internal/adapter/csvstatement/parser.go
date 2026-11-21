// Package csvstatement lê o extrato de liquidação do provedor em CSV.
package csvstatement

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
)

// Colunas obrigatórias. A ordem no arquivo é livre; o cabeçalho define.
var requiredColumns = []string{"transaction_id", "payment_id", "type", "amount", "currency", "occurred_at"}

// ErrInvalidStatement indica um extrato que não pode ser conciliado com segurança.
var ErrInvalidStatement = errors.New("invalid statement")

// Parse lê o extrato inteiro. Uma linha inválida aborta a leitura com o
// número da linha: conciliar um extrato parcial esconderia divergências.
func Parse(r io.Reader) ([]domain.StatementLine, error) {
	reader := csv.NewReader(r)
	reader.TrimLeadingSpace = true

	header, err := reader.Read()
	if errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: empty file", ErrInvalidStatement)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: read header: %w", ErrInvalidStatement, err)
	}
	index, err := columnIndex(header)
	if err != nil {
		return nil, err
	}

	var lines []domain.StatementLine
	for lineNumber := 2; ; lineNumber++ {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return lines, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%w: line %d: %w", ErrInvalidStatement, lineNumber, err)
		}
		line, err := parseRecord(record, index)
		if err != nil {
			return nil, fmt.Errorf("%w: line %d: %w", ErrInvalidStatement, lineNumber, err)
		}
		line.LineNumber = lineNumber
		lines = append(lines, line)
	}
}

func columnIndex(header []string) (map[string]int, error) {
	index := make(map[string]int, len(header))
	for i, name := range header {
		index[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(name, "\ufeff")))] = i
	}
	for _, col := range requiredColumns {
		if _, ok := index[col]; !ok {
			return nil, fmt.Errorf("%w: missing column %q", ErrInvalidStatement, col)
		}
	}
	return index, nil
}

func parseRecord(record []string, index map[string]int) (domain.StatementLine, error) {
	field := func(name string) string { return strings.TrimSpace(record[index[name]]) }

	var line domain.StatementLine
	line.TransactionID = field("transaction_id")
	line.PaymentID = field("payment_id")
	if line.TransactionID == "" || line.PaymentID == "" {
		return line, errors.New("transaction_id and payment_id are required")
	}

	switch t := field("type"); t {
	case "capture":
		line.Direction = domain.Credit
	case "refund":
		line.Direction = domain.Debit
	default:
		return line, fmt.Errorf("unknown type %q (expected capture or refund)", t)
	}

	amount, err := domain.ParseAmountCents(field("amount"))
	if err != nil {
		return line, err
	}
	if amount == 0 {
		return line, errors.New("amount must be positive")
	}
	line.AmountCents = amount

	if line.Currency, err = domain.ParseCurrency(field("currency")); err != nil {
		return line, err
	}

	occurredAt, err := time.Parse(time.RFC3339, field("occurred_at"))
	if err != nil {
		return line, fmt.Errorf("occurred_at must be RFC 3339: %w", err)
	}
	line.OccurredAt = occurredAt.UTC()
	return line, nil
}
