package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func validMessage(t *testing.T) []byte {
	t.Helper()
	b, err := json.Marshal(LedgerEntryRecorded{
		SchemaVersion: 1, EventID: "evt_1", TransactionID: "txn_1", PaymentID: "pay_1",
		Direction: "credit", AmountCents: 1000, Currency: "BRL", OccurredAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestProjectorAppliesOnce(t *testing.T) {
	t.Parallel()
	repo := &fakeProjection{}
	p := NewBalanceProjector("balance-projector", repo, discardLogger())
	body := validMessage(t)

	for range 3 {
		if err := p.Handle(context.Background(), "evt_1", body); err != nil {
			t.Fatalf("Handle: %v", err)
		}
	}
	if len(repo.applied) != 1 {
		t.Fatalf("applied %d times, want 1", len(repo.applied))
	}
}

func TestProjectorClassifiesErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		messageID string
		body      []byte
		repoErr   error
		permanent bool
	}{
		{name: "sem message id", messageID: "", body: validMessage(t), permanent: true},
		{name: "json inválido", messageID: "m1", body: []byte(`{`), permanent: true},
		{name: "versão desconhecida", messageID: "m2", body: []byte(`{"schema_version":2}`), permanent: true},
		{name: "direção inválida", messageID: "m3",
			body:      []byte(`{"schema_version":1,"payment_id":"p","transaction_id":"t","direction":"x","amount_cents":1}`),
			permanent: true},
		{name: "valor zero", messageID: "m4",
			body:      []byte(`{"schema_version":1,"payment_id":"p","transaction_id":"t","direction":"debit","amount_cents":0}`),
			permanent: true},
		{name: "banco fora", messageID: "m5", body: validMessage(t), repoErr: errors.New("db down"), permanent: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := NewBalanceProjector("c", &fakeProjection{err: tt.repoErr}, discardLogger())
			err := p.Handle(context.Background(), tt.messageID, tt.body)
			if err == nil {
				t.Fatal("expected error")
			}
			if IsPermanent(err) != tt.permanent {
				t.Fatalf("IsPermanent(%v) = %v, want %v", err, IsPermanent(err), tt.permanent)
			}
		})
	}
}
