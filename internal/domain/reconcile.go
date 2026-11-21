package domain

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"
)

// StatementLine é uma linha do extrato de liquidação enviado pelo provedor.
type StatementLine struct {
	LineNumber    int
	TransactionID string
	PaymentID     string
	Direction     Direction
	AmountCents   int64
	Currency      Currency
	OccurredAt    time.Time
}

// DivergenceKind classifica uma divergência entre extrato e ledger.
type DivergenceKind string

// Tipos de divergência produzidos pela conciliação.
const (
	// MissingInLedger: o provedor liquidou, mas o ledger não registrou (faltando).
	MissingInLedger DivergenceKind = "missing_in_ledger"
	// MissingInStatement: o ledger registrou, mas o provedor não liquidou (sobrando).
	MissingInStatement DivergenceKind = "missing_in_statement"
	// AmountMismatch: a transação existe nos dois lados com valor, moeda ou direção diferentes.
	AmountMismatch DivergenceKind = "amount_mismatch"
	// DuplicateInStatement: a mesma transação aparece mais de uma vez no extrato.
	DuplicateInStatement DivergenceKind = "duplicate_in_statement"
)

// Divergence descreve uma diferença encontrada na conciliação.
type Divergence struct {
	Kind          DivergenceKind
	TransactionID string
	Statement     *StatementLine
	Ledger        *LedgerEntry
	Detail        string
}

// Period é um intervalo semiaberto [From, To) em UTC.
type Period struct {
	From time.Time
	To   time.Time
}

// Contains informa se t pertence ao período.
func (p Period) Contains(t time.Time) bool {
	return !t.Before(p.From) && t.Before(p.To)
}

// Report é o resultado de uma conciliação.
type Report struct {
	Period             Period
	StatementLines     int
	LedgerEntries      int
	Matched            int
	SkippedOutOfPeriod int
	StatementNetCents  int64
	LedgerNetCents     int64
	Divergences        []Divergence
}

// HasDivergences informa se a conciliação encontrou alguma diferença.
func (r Report) HasDivergences() bool { return len(r.Divergences) > 0 }

// CountByKind agrega as divergências por tipo.
func (r Report) CountByKind() map[DivergenceKind]int {
	counts := make(map[DivergenceKind]int, 4)
	for _, d := range r.Divergences {
		counts[d.Kind]++
	}
	return counts
}

// Reconcile compara o extrato do provedor com os lançamentos do ledger no
// período, casando por TransactionID. Linhas do extrato fora do período são
// ignoradas e contadas em SkippedOutOfPeriod. O resultado é determinístico:
// divergências ordenadas por tipo e depois por transação.
func Reconcile(period Period, statement []StatementLine, ledger []LedgerEntry) Report {
	report := Report{Period: period}

	byTx := make(map[string]LedgerEntry, len(ledger))
	for _, e := range ledger {
		if !period.Contains(e.OccurredAt) {
			continue
		}
		byTx[e.TransactionID] = e
		report.LedgerEntries++
		report.LedgerNetCents += e.SignedCents()
	}

	seen := make(map[string]bool, len(statement))
	for i := range statement {
		line := statement[i]
		if !period.Contains(line.OccurredAt) {
			report.SkippedOutOfPeriod++
			continue
		}
		report.StatementLines++
		if seen[line.TransactionID] {
			report.Divergences = append(report.Divergences, Divergence{
				Kind:          DuplicateInStatement,
				TransactionID: line.TransactionID,
				Statement:     &line,
				Detail:        fmt.Sprintf("linha %d repete uma transação já listada", line.LineNumber),
			})
			continue
		}
		seen[line.TransactionID] = true
		report.StatementNetCents += line.signedCents()

		entry, ok := byTx[line.TransactionID]
		if !ok {
			report.Divergences = append(report.Divergences, Divergence{
				Kind:          MissingInLedger,
				TransactionID: line.TransactionID,
				Statement:     &line,
				Detail:        "transação liquidada pelo provedor sem lançamento no ledger",
			})
			continue
		}
		if detail := compare(line, entry); detail != "" {
			report.Divergences = append(report.Divergences, Divergence{
				Kind:          AmountMismatch,
				TransactionID: line.TransactionID,
				Statement:     &line,
				Ledger:        &entry,
				Detail:        detail,
			})
			continue
		}
		report.Matched++
	}

	for tx, entry := range byTx {
		if seen[tx] {
			continue
		}
		report.Divergences = append(report.Divergences, Divergence{
			Kind:          MissingInStatement,
			TransactionID: tx,
			Ledger:        &entry,
			Detail:        "lançamento no ledger sem liquidação no extrato",
		})
	}

	slices.SortFunc(report.Divergences, func(a, b Divergence) int {
		return cmp.Or(
			cmp.Compare(a.Kind, b.Kind),
			cmp.Compare(a.TransactionID, b.TransactionID),
			cmp.Compare(lineNumber(a), lineNumber(b)),
		)
	})
	return report
}

func compare(line StatementLine, entry LedgerEntry) string {
	var diffs []string
	if line.AmountCents != entry.AmountCents {
		diffs = append(diffs, fmt.Sprintf("valor: extrato %s, ledger %s",
			FormatCents(line.AmountCents), FormatCents(entry.AmountCents)))
	}
	if line.Currency != entry.Currency {
		diffs = append(diffs, fmt.Sprintf("moeda: extrato %s, ledger %s", line.Currency, entry.Currency))
	}
	if line.Direction != entry.Direction {
		diffs = append(diffs, fmt.Sprintf("direção: extrato %s, ledger %s", line.Direction, entry.Direction))
	}
	return strings.Join(diffs, "; ")
}

func (l StatementLine) signedCents() int64 {
	if l.Direction == Debit {
		return -l.AmountCents
	}
	return l.AmountCents
}

func lineNumber(d Divergence) int {
	if d.Statement == nil {
		return 0
	}
	return d.Statement.LineNumber
}
