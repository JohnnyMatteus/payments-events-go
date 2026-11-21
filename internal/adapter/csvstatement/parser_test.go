package csvstatement_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/csvstatement"
	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
)

func TestParseValidStatement(t *testing.T) {
	t.Parallel()
	in := "\ufeffoccurred_at,transaction_id,payment_id,type,amount,currency,fee\n" +
		"2026-11-20T09:15:00-03:00,txn_1,pay_1,capture,150.00,brl,3.50\n" +
		"2026-11-20T14:00:00Z, txn_2 ,pay_1,refund,25.5,BRL,0\n"

	lines, err := csvstatement.Parse(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.StatementLine{
		{LineNumber: 2, TransactionID: "txn_1", PaymentID: "pay_1", Direction: domain.Credit,
			AmountCents: 15000, Currency: "BRL", OccurredAt: time.Date(2026, 11, 20, 12, 15, 0, 0, time.UTC)},
		{LineNumber: 3, TransactionID: "txn_2", PaymentID: "pay_1", Direction: domain.Debit,
			AmountCents: 2550, Currency: "BRL", OccurredAt: time.Date(2026, 11, 20, 14, 0, 0, 0, time.UTC)},
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines", len(lines))
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %+v, want %+v", i, lines[i], want[i])
		}
	}
}

func TestParseHeaderOnly(t *testing.T) {
	t.Parallel()
	lines, err := csvstatement.Parse(strings.NewReader("transaction_id,payment_id,type,amount,currency,occurred_at\n"))
	if err != nil || len(lines) != 0 {
		t.Fatalf("lines=%v err=%v", lines, err)
	}
}

func TestParseErrors(t *testing.T) {
	t.Parallel()
	const header = "transaction_id,payment_id,type,amount,currency,occurred_at\n"
	tests := []struct {
		name    string
		in      string
		wantMsg string
	}{
		{name: "arquivo vazio", in: "", wantMsg: "empty file"},
		{name: "coluna faltando", in: "transaction_id,payment_id,type,amount,currency\n", wantMsg: `missing column "occurred_at"`},
		{name: "tipo desconhecido", in: header + "t,p,chargeback,1.00,BRL,2026-11-20T00:00:00Z\n", wantMsg: "line 2: unknown type"},
		{name: "valor com vírgula", in: header + "t,p,capture,1,00,BRL,2026-11-20T00:00:00Z\n", wantMsg: "line 2"},
		{name: "valor inválido", in: header + "t,p,capture,abc,BRL,2026-11-20T00:00:00Z\n", wantMsg: "invalid amount"},
		{name: "valor zero", in: header + "t,p,capture,0.00,BRL,2026-11-20T00:00:00Z\n", wantMsg: "amount must be positive"},
		{name: "moeda inválida", in: header + "t,p,capture,1.00,R$,2026-11-20T00:00:00Z\n", wantMsg: "invalid currency"},
		{name: "data inválida", in: header + "t,p,capture,1.00,BRL,20/11/2026\n", wantMsg: "RFC 3339"},
		{name: "sem transação", in: header + ",p,capture,1.00,BRL,2026-11-20T00:00:00Z\n", wantMsg: "required"},
		{name: "erro na terceira linha", in: header + "t,p,capture,1.00,BRL,2026-11-20T00:00:00Z\nt2,p,capture,x,BRL,2026-11-20T00:00:00Z\n", wantMsg: "line 3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := csvstatement.Parse(strings.NewReader(tt.in))
			if !errors.Is(err, csvstatement.ErrInvalidStatement) {
				t.Fatalf("err = %v, want ErrInvalidStatement", err)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Fatalf("err = %q, want it to contain %q", err, tt.wantMsg)
			}
		})
	}
}
