package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JohnnyMatteus/payments-events-go/internal/app"
	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
)

// SQLComparer implementa app.LedgerComparer: a junção entre o extrato e o
// ledger, a soma dos ajustes por transação e o recorte do período acontecem no
// PostgreSQL, então os lançamentos do período nunca vão para a memória. O
// resultado é o mesmo de domain.Reconcile (há teste de equivalência).
type SQLComparer struct {
	pool           *pgxpool.Pool
	includeMatches bool
}

var _ app.LedgerComparer = (*SQLComparer)(nil)

// NewSQLComparer cria o comparador. Com includeMatches falso o relatório traz só
// a contagem de pares conciliados (Matched), sem o slice Matches: é o que
// poupa memória quando há milhões de linhas.
func NewSQLComparer(pool *pgxpool.Pool, includeMatches bool) *SQLComparer {
	return &SQLComparer{pool: pool, includeMatches: includeMatches}
}

// Só as primeiras ocorrências de cada transação, dentro do período, vão para a
// junção. O lançamento efetivo de uma transação soma o provedor e os ajustes que
// a corrigem; a identidade (evento, pagamento, data) vem do lançamento do
// provedor e, se não houver, do primeiro ajuste, como em domain.effectiveEntries.
const compareQuery = `
WITH base AS (
	SELECT id, event_id, COALESCE(merchant_id, '') AS merchant_id, payment_id, currency, occurred_at,
		direction, amount_cents,
		COALESCE(adjusts_transaction_id, transaction_id) AS k,
		adjusts_transaction_id IS NOT NULL AS is_adj
	FROM ledger_entries
	WHERE occurred_at >= $1 AND occurred_at < $2
), agg AS (
	SELECT k, SUM(CASE direction WHEN 'credit' THEN amount_cents ELSE -amount_cents END)::bigint AS net,
		bool_or(is_adj) AS adjusted
	FROM base GROUP BY k
), ident AS (
	SELECT DISTINCT ON (k) k, event_id, merchant_id, payment_id, currency, occurred_at
	FROM base ORDER BY k, is_adj, occurred_at, id
), eff AS (
	SELECT i.k, i.event_id, i.merchant_id, i.payment_id, i.currency, i.occurred_at, a.net, a.adjusted
	FROM ident i JOIN agg a ON a.k = i.k
)
SELECT s.ord, e.k, e.event_id, e.merchant_id, e.payment_id, e.currency, e.occurred_at, e.net, e.adjusted
FROM reconcile_statement s
FULL OUTER JOIN eff e ON e.k = s.transaction_id`

// Reconcile compara o extrato com o ledger do período no banco.
func (c *SQLComparer) Reconcile(ctx context.Context, period domain.Period, statement []domain.StatementLine) (domain.Report, error) {
	report := domain.Report{Period: period}

	// Recorte do período, contagens e duplicatas no extrato: já está em memória,
	// é barato e mantém as mensagens idênticas às da comparação em memória.
	seen := make(map[string]bool, len(statement))
	var forJoin [][]any
	for i := range statement {
		line := statement[i]
		if !period.Contains(line.OccurredAt) {
			report.SkippedOutOfPeriod++
			continue
		}
		report.StatementLines++
		if seen[line.TransactionID] {
			report.Divergences = append(report.Divergences, domain.Divergence{
				Kind:          domain.DuplicateInStatement,
				TransactionID: line.TransactionID,
				Statement:     &line,
				Detail:        fmt.Sprintf("linha %d repete uma transação já listada", line.LineNumber),
			})
			continue
		}
		seen[line.TransactionID] = true
		report.StatementNetCents += line.SignedCents()
		forJoin = append(forJoin, []any{int64(i), line.TransactionID})
	}

	err := pgx.BeginFunc(ctx, c.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `CREATE TEMP TABLE reconcile_statement (ord bigint NOT NULL, transaction_id text NOT NULL) ON COMMIT DROP`); err != nil {
			return fmt.Errorf("create statement table: %w", err)
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"reconcile_statement"}, []string{"ord", "transaction_id"},
			pgx.CopyFromRows(forJoin)); err != nil {
			return fmt.Errorf("copy statement lines: %w", err)
		}
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(CASE direction WHEN 'credit' THEN amount_cents ELSE -amount_cents END), 0)::bigint
			FROM ledger_entries WHERE occurred_at >= $1 AND occurred_at < $2`, period.From, period.To).
			Scan(&report.LedgerNetCents); err != nil {
			return fmt.Errorf("sum ledger: %w", err)
		}
		rows, err := tx.Query(ctx, compareQuery, period.From, period.To)
		if err != nil {
			return fmt.Errorf("compare in database: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var (
				ord                                       *int64
				key, eventID, merchantID, paymentID, curr *string
				occurredAt                                *time.Time
				net                                       *int64
				adjusted                                  *bool
			)
			if err := rows.Scan(&ord, &key, &eventID, &merchantID, &paymentID, &curr, &occurredAt, &net, &adjusted); err != nil {
				return fmt.Errorf("scan comparison row: %w", err)
			}
			var entry *domain.LedgerEntry
			if key != nil {
				report.LedgerEntries++
				e := domain.LedgerEntry{
					EventID: *eventID, MerchantID: *merchantID, TransactionID: *key, PaymentID: *paymentID,
					Direction: domain.Credit, AmountCents: *net, Currency: domain.Currency(*curr), OccurredAt: occurredAt.UTC(),
				}
				if *net < 0 {
					e.Direction, e.AmountCents = domain.Debit, -*net
				}
				entry = &e
			}
			c.classify(&report, statement, ord, entry, adjusted != nil && *adjusted)
		}
		return rows.Err()
	})
	if err != nil {
		return domain.Report{}, err
	}
	report.Finalize()
	return report, nil
}

// classify aplica a mesma regra de domain.Reconcile a uma linha da junção.
func (c *SQLComparer) classify(report *domain.Report, statement []domain.StatementLine, ord *int64, entry *domain.LedgerEntry, adjusted bool) {
	switch {
	case ord != nil && entry == nil:
		line := statement[int(*ord)]
		report.Divergences = append(report.Divergences, domain.Divergence{
			Kind:          domain.MissingInLedger,
			TransactionID: line.TransactionID,
			Statement:     &line,
			Detail:        "transação liquidada pelo provedor sem lançamento no ledger",
		})
	case ord != nil:
		line := statement[int(*ord)]
		if detail := domain.CompareLine(line, *entry); detail != "" {
			report.Divergences = append(report.Divergences, domain.Divergence{
				Kind:          domain.AmountMismatch,
				TransactionID: line.TransactionID,
				Statement:     &line,
				Ledger:        entry,
				Detail:        detail,
			})
			return
		}
		report.Matched++
		if c.includeMatches {
			report.Matches = append(report.Matches, domain.Match{Statement: line, Ledger: *entry, Adjusted: adjusted})
		}
	default:
		// Estornado por ajuste: o ledger reconhece que o provedor não liquidou.
		if entry.AmountCents == 0 {
			report.Reversed++
			return
		}
		report.Divergences = append(report.Divergences, domain.Divergence{
			Kind:          domain.MissingInStatement,
			TransactionID: entry.TransactionID,
			Ledger:        entry,
			Detail:        "lançamento no ledger sem liquidação no extrato",
		})
	}
}
