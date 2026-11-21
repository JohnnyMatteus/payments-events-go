// Package reportout renderiza o relatório de conciliação em texto ou JSON.
package reportout

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
)

// kindLabels traduz os tipos de divergência para o relatório em texto.
var kindLabels = map[domain.DivergenceKind]string{
	domain.MissingInLedger:      "faltando no ledger",
	domain.MissingInStatement:   "sobrando no ledger",
	domain.AmountMismatch:       "valor divergente",
	domain.DuplicateInStatement: "duplicada no extrato",
}

var kindOrder = []domain.DivergenceKind{
	domain.MissingInLedger, domain.MissingInStatement, domain.AmountMismatch, domain.DuplicateInStatement,
}

type jsonReport struct {
	PeriodFrom         time.Time      `json:"period_from"`
	PeriodTo           time.Time      `json:"period_to"`
	StatementLines     int            `json:"statement_lines"`
	LedgerEntries      int            `json:"ledger_entries"`
	Matched            int            `json:"matched"`
	SkippedOutOfPeriod int            `json:"skipped_out_of_period"`
	StatementNet       string         `json:"statement_net"`
	LedgerNet          string         `json:"ledger_net"`
	Counts             map[string]int `json:"divergence_counts"`
	Divergences        []jsonDiv      `json:"divergences"`
}

type jsonDiv struct {
	Kind            string `json:"kind"`
	TransactionID   string `json:"transaction_id"`
	StatementLine   int    `json:"statement_line,omitempty"`
	StatementAmount string `json:"statement_amount,omitempty"`
	LedgerAmount    string `json:"ledger_amount,omitempty"`
	Detail          string `json:"detail"`
}

// WriteJSON escreve o relatório em JSON, pronto para outro sistema consumir.
func WriteJSON(w io.Writer, r domain.Report) error {
	out := jsonReport{
		PeriodFrom: r.Period.From, PeriodTo: r.Period.To,
		StatementLines: r.StatementLines, LedgerEntries: r.LedgerEntries,
		Matched: r.Matched, SkippedOutOfPeriod: r.SkippedOutOfPeriod,
		StatementNet: domain.FormatCents(r.StatementNetCents), LedgerNet: domain.FormatCents(r.LedgerNetCents),
		Counts:      map[string]int{},
		Divergences: make([]jsonDiv, 0, len(r.Divergences)),
	}
	for kind, n := range r.CountByKind() {
		out.Counts[string(kind)] = n
	}
	for _, d := range r.Divergences {
		jd := jsonDiv{Kind: string(d.Kind), TransactionID: d.TransactionID, Detail: d.Detail}
		if d.Statement != nil {
			jd.StatementLine = d.Statement.LineNumber
			jd.StatementAmount = amount(d.Statement.AmountCents, d.Statement.Currency)
		}
		if d.Ledger != nil {
			jd.LedgerAmount = amount(d.Ledger.AmountCents, d.Ledger.Currency)
		}
		out.Divergences = append(out.Divergences, jd)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// WriteText escreve o relatório legível para pessoas (operação financeira).
func WriteText(w io.Writer, r domain.Report) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "Conciliação PagaFácil Sandbox\t%s a %s (UTC)\n",
		r.Period.From.Format(time.DateOnly), r.Period.To.Add(-time.Nanosecond).Format(time.DateOnly))
	fmt.Fprintf(tw, "Linhas do extrato\t%d\n", r.StatementLines)
	fmt.Fprintf(tw, "Lançamentos no ledger\t%d\n", r.LedgerEntries)
	fmt.Fprintf(tw, "Conciliadas\t%d\n", r.Matched)
	fmt.Fprintf(tw, "Fora do período (ignoradas)\t%d\n", r.SkippedOutOfPeriod)
	fmt.Fprintf(tw, "Líquido extrato / ledger\t%s / %s\n",
		domain.FormatCents(r.StatementNetCents), domain.FormatCents(r.LedgerNetCents))
	counts := r.CountByKind()
	for _, kind := range kindOrder {
		fmt.Fprintf(tw, "Divergências: %s\t%d\n", kindLabels[kind], counts[kind])
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if !r.HasDivergences() {
		_, err := fmt.Fprintln(w, "\nNenhuma divergência encontrada.")
		return err
	}

	fmt.Fprintln(w)
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TIPO\tTRANSAÇÃO\tLINHA\tEXTRATO\tLEDGER\tDETALHE")
	for _, d := range r.Divergences {
		line, stmt, ledger := "-", "-", "-"
		if d.Statement != nil {
			line = fmt.Sprint(d.Statement.LineNumber)
			stmt = amount(d.Statement.AmountCents, d.Statement.Currency)
		}
		if d.Ledger != nil {
			ledger = amount(d.Ledger.AmountCents, d.Ledger.Currency)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", kindLabels[d.Kind], d.TransactionID, line, stmt, ledger, d.Detail)
	}
	return tw.Flush()
}

func amount(cents int64, currency domain.Currency) string {
	return domain.FormatCents(cents) + " " + string(currency)
}
