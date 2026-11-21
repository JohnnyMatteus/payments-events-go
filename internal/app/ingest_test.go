package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
)

func captureEvent() domain.Event {
	return domain.Event{
		ID: "evt_1", Type: domain.EventPaymentCaptured, TransactionID: "txn_1", PaymentID: "pay_1",
		AmountCents: 15000, Currency: "BRL", OccurredAt: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC),
	}
}

func TestIngestMonetaryEventBuildsEntryAndOutboxMessage(t *testing.T) {
	t.Parallel()
	rec := &fakeRecorder{outcome: OutcomeRecorded}
	svc := NewIngestService(rec)

	outcome, err := svc.Ingest(context.Background(), captureEvent(), []byte(`{"raw":true}`))
	if err != nil || outcome != OutcomeRecorded {
		t.Fatalf("Ingest = %q, %v", outcome, err)
	}
	if len(rec.got) != 1 {
		t.Fatalf("recorder called %d times", len(rec.got))
	}
	got := rec.got[0]
	if got.Entry == nil || got.Entry.Direction != domain.Credit || got.Entry.AmountCents != 15000 {
		t.Fatalf("entry = %+v", got.Entry)
	}
	if got.Message == nil || got.Message.MessageID != "evt_1" || got.Message.RoutingKey != RoutingKeyLedgerEntryRecorded {
		t.Fatalf("message = %+v", got.Message)
	}
	var payload LedgerEntryRecorded
	if err := json.Unmarshal(got.Message.Payload, &payload); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	if err := payload.Validate(); err != nil || payload.TransactionID != "txn_1" {
		t.Fatalf("payload = %+v, validate err %v", payload, err)
	}
	if string(got.RawPayload) != `{"raw":true}` {
		t.Fatalf("raw payload not preserved: %s", got.RawPayload)
	}
}

func TestIngestNonMonetaryEventHasNoEntry(t *testing.T) {
	t.Parallel()
	rec := &fakeRecorder{outcome: OutcomeIgnored}
	svc := NewIngestService(rec)

	outcome, err := svc.Ingest(context.Background(), domain.Event{ID: "evt_2", Type: "payment.dispute_opened"}, nil)
	if err != nil || outcome != OutcomeIgnored {
		t.Fatalf("Ingest = %q, %v", outcome, err)
	}
	if rec.got[0].Entry != nil || rec.got[0].Message != nil {
		t.Fatalf("non-monetary event must not produce entry or message: %+v", rec.got[0])
	}
}

func TestIngestRejectsInvalidEventWithoutTouchingStorage(t *testing.T) {
	t.Parallel()
	rec := &fakeRecorder{}
	ev := captureEvent()
	ev.AmountCents = 0

	_, err := NewIngestService(rec).Ingest(context.Background(), ev, nil)
	if !errors.Is(err, domain.ErrInvalidEvent) {
		t.Fatalf("err = %v, want ErrInvalidEvent", err)
	}
	if len(rec.got) != 0 {
		t.Fatal("invalid event must not reach the recorder")
	}
}

func TestIngestPropagatesStorageErrors(t *testing.T) {
	t.Parallel()
	boom := errors.New("connection refused")
	_, err := NewIngestService(&fakeRecorder{err: boom}).Ingest(context.Background(), captureEvent(), nil)
	if !errors.Is(err, boom) || errors.Is(err, domain.ErrInvalidEvent) {
		t.Fatalf("err = %v, want wrapped storage error", err)
	}
}
