package reportout_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/reportout"
	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
)

func sampleReport() domain.Report {
	from := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	period := domain.Period{From: from, To: from.AddDate(0, 0, 1)}
	statement := []domain.StatementLine{
		{LineNumber: 2, TransactionID: "txn_missing", Direction: domain.Credit, AmountCents: 5000, Currency: "BRL", OccurredAt: from},
		{LineNumber: 3, TransactionID: "txn_amount", Direction: domain.Credit, AmountCents: 9990, Currency: "BRL", OccurredAt: from},
	}
	ledger := []domain.LedgerEntry{
		{TransactionID: "txn_amount", Direction: domain.Credit, AmountCents: 10000, Currency: "BRL", OccurredAt: from},
		{TransactionID: "txn_extra", Direction: domain.Debit, AmountCents: 300, Currency: "BRL", OccurredAt: from},
	}
	return domain.Reconcile(period, statement, ledger)
}

func TestWriteText(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := reportout.WriteText(&buf, sampleReport()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"2026-11-20 a 2026-11-20 (UTC)",
		"faltando no ledger    1",
		"sobrando no ledger    1",
		"valor divergente      1",
		"txn_amount", "99.90 BRL", "100.00 BRL",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("text report missing %q:\n%s", want, out)
		}
	}
}

func TestWriteTextWithoutDivergences(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := reportout.WriteText(&buf, domain.Report{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "Nenhuma divergência encontrada.") {
		t.Fatalf("output:\n%s", buf.String())
	}
}

func TestWriteJSON(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := reportout.WriteJSON(&buf, sampleReport()); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Counts      map[string]int `json:"divergence_counts"`
		LedgerNet   string         `json:"ledger_net"`
		Divergences []struct {
			Kind            string `json:"kind"`
			StatementLine   int    `json:"statement_line"`
			StatementAmount string `json:"statement_amount"`
			LedgerAmount    string `json:"ledger_amount"`
		} `json:"divergences"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if got.Counts["amount_mismatch"] != 1 || got.Counts["missing_in_ledger"] != 1 || got.Counts["missing_in_statement"] != 1 {
		t.Fatalf("counts = %v", got.Counts)
	}
	if got.LedgerNet != "97.00" {
		t.Fatalf("ledger_net = %s", got.LedgerNet)
	}
	first := got.Divergences[0]
	if first.Kind != "amount_mismatch" || first.StatementLine != 3 || first.StatementAmount != "99.90 BRL" || first.LedgerAmount != "100.00 BRL" {
		t.Fatalf("first divergence = %+v", first)
	}
}
