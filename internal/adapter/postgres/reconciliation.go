package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JohnnyMatteus/payments-events-go/internal/app"
	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
)

// ReconciliationStore implementa app.ReconciliationStore.
type ReconciliationStore struct {
	pool *pgxpool.Pool
}

// NewReconciliationStore cria o repositório das execuções.
func NewReconciliationStore(pool *pgxpool.Pool) *ReconciliationStore {
	return &ReconciliationStore{pool: pool}
}

// SaveRun grava a execução, as linhas da partida dobrada e atualiza os casos
// de divergência na mesma transação.
func (s *ReconciliationStore) SaveRun(ctx context.Context, rec app.RunRecord, report *domain.Report) (app.RunSummary, error) {
	summary := app.RunSummary{
		Date: rec.Date, StatementRef: rec.Statement.Ref, Trigger: rec.Trigger, TriggeredBy: rec.TriggeredBy,
		Status: "succeeded", StartedAt: rec.StartedAt, FinishedAt: rec.FinishedAt, Counts: map[string]int{},
	}
	if rec.Err != nil {
		summary.Status, summary.Error = "failed", errorText(rec.Err)
	}
	if report != nil {
		summary.StatementLines, summary.LedgerEntries = report.StatementLines, report.LedgerEntries
		summary.Matched, summary.Reversed = report.Matched, report.Reversed
		summary.StatementNet, summary.LedgerNet = report.StatementNetCents, report.LedgerNetCents
		for k, n := range report.CountByKind() {
			summary.Counts[string(k)] = n
		}
	}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			INSERT INTO reconciliation_runs (settlement_date, statement_ref, trigger, triggered_by, status, error,
				statement_lines, ledger_entries, matched, reversed, statement_net, ledger_net, started_at, finished_at)
			VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8, $9, $10, $11, $12, $13, $14)
			RETURNING id`,
			rec.Date, summary.StatementRef, string(rec.Trigger), rec.TriggeredBy, summary.Status, summary.Error,
			summary.StatementLines, summary.LedgerEntries, summary.Matched, summary.Reversed, summary.StatementNet,
			summary.LedgerNet, rec.StartedAt, rec.FinishedAt).Scan(&summary.ID)
		if err != nil {
			return fmt.Errorf("insert run: %w", err)
		}
		if report == nil {
			return nil
		}
		if err := insertItems(ctx, tx, summary.ID, app.ItemsOf(*report)); err != nil {
			return err
		}
		return syncCases(ctx, tx, summary.ID, rec.Date, report.Divergences)
	})
	if err != nil {
		return app.RunSummary{}, fmt.Errorf("save reconciliation run: %w", err)
	}
	return summary, nil
}

func insertItems(ctx context.Context, tx pgx.Tx, runID int64, items []app.RunItem) error {
	batch := &pgx.Batch{}
	for i, it := range items {
		stmt, err := marshalNullable(it.Statement)
		if err != nil {
			return err
		}
		led, err := marshalNullable(it.Ledger)
		if err != nil {
			return err
		}
		batch.Queue(`
			INSERT INTO reconciliation_items (run_id, position, kind, transaction_id, statement, ledger, adjusted, delta_cents, detail)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			runID, i, it.Kind, it.TransactionID, stmt, led, it.Adjusted, it.DeltaCents, it.Detail)
	}
	return runBatch(ctx, tx, batch, "insert run items")
}

func runBatch(ctx context.Context, tx pgx.Tx, batch *pgx.Batch, what string) error {
	res := tx.SendBatch(ctx, batch)
	for range batch.Len() {
		if _, err := res.Exec(); err != nil {
			_ = res.Close()
			return fmt.Errorf("%s: %w", what, err)
		}
	}
	return res.Close()
}

func marshalNullable(v any) ([]byte, error) {
	switch x := v.(type) {
	case *domain.StatementLine:
		if x == nil {
			return nil, nil
		}
	case *domain.LedgerEntry:
		if x == nil {
			return nil, nil
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal run item side: %w", err)
	}
	return b, nil
}

// syncCases cria ou atualiza o caso de cada divergência da execução e
// resolve os que sumiram (o ledger passou a bater com o extrato). Casos
// justificados ficam como estão: a decisão humana vale mais que a execução.
func syncCases(ctx context.Context, tx pgx.Tx, runID int64, date time.Time, divs []domain.Divergence) error {
	keys := make([]string, 0, len(divs))
	batch := &pgx.Batch{}
	for _, d := range divs {
		stmt, err := marshalNullable(d.Statement)
		if err != nil {
			return err
		}
		led, err := marshalNullable(d.Ledger)
		if err != nil {
			return err
		}
		keys = append(keys, string(d.Kind)+"|"+d.TransactionID)
		batch.Queue(`
			INSERT INTO divergence_cases (settlement_date, transaction_id, kind, statement, ledger, delta_cents, detail, first_run_id, last_run_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
			ON CONFLICT (settlement_date, transaction_id, kind) DO UPDATE SET
				statement = EXCLUDED.statement, ledger = EXCLUDED.ledger, delta_cents = EXCLUDED.delta_cents,
				detail = EXCLUDED.detail, last_run_id = EXCLUDED.last_run_id, updated_at = now(),
				status = CASE WHEN divergence_cases.status = 'resolved' THEN 'open' ELSE divergence_cases.status END,
				resolved_by = CASE WHEN divergence_cases.status = 'resolved' THEN NULL ELSE divergence_cases.resolved_by END,
				resolved_at = CASE WHEN divergence_cases.status = 'resolved' THEN NULL ELSE divergence_cases.resolved_at END`,
			date, d.TransactionID, string(d.Kind), stmt, led, d.DeltaCents(), d.Detail, runID)
	}
	if err := runBatch(ctx, tx, batch, "upsert cases"); err != nil {
		return err
	}
	if keys == nil {
		keys = []string{}
	}
	rows, err := tx.Query(ctx, `
		UPDATE divergence_cases SET status = 'resolved', resolved_by = 'reconciliation', resolved_at = now(), updated_at = now()
		WHERE settlement_date = $1 AND status IN ('open', 'adjustment_proposed') AND NOT (kind || '|' || transaction_id = ANY($2))
		RETURNING id`, date, keys)
	if err != nil {
		return fmt.Errorf("resolve vanished cases: %w", err)
	}
	vanished, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return fmt.Errorf("scan vanished cases: %w", err)
	}
	if len(vanished) == 0 {
		return nil
	}
	_, err = tx.Exec(ctx, `
		UPDATE ledger_adjustments SET status = 'rejected', decided_by = 'reconciliation', decided_at = now(),
			decision_note = 'a divergência deixou de existir na conciliação'
		WHERE status = 'proposed' AND case_id = ANY($1)`, vanished)
	if err != nil {
		return fmt.Errorf("reject orphan adjustments: %w", err)
	}
	return nil
}

const runColumns = `r.id, r.settlement_date, r.statement_ref, r.trigger, r.triggered_by, r.status, COALESCE(r.error, ''),
	r.statement_lines, r.ledger_entries, r.matched, r.reversed, r.statement_net, r.ledger_net, r.started_at, r.finished_at`

func (s *ReconciliationStore) scanRuns(ctx context.Context, query string, args ...any) ([]app.RunSummary, error) {
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query runs: %w", err)
	}
	runs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (app.RunSummary, error) {
		var (
			r       app.RunSummary
			trigger string
		)
		err := row.Scan(&r.ID, &r.Date, &r.StatementRef, &trigger, &r.TriggeredBy, &r.Status, &r.Error,
			&r.StatementLines, &r.LedgerEntries, &r.Matched, &r.Reversed, &r.StatementNet, &r.LedgerNet,
			&r.StartedAt, &r.FinishedAt)
		r.Trigger = app.RunTrigger(trigger)
		r.Date = r.Date.UTC()
		r.Counts = map[string]int{}
		return r, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan runs: %w", err)
	}
	if len(runs) == 0 {
		return runs, nil
	}
	ids := make([]int64, len(runs))
	index := make(map[int64]*app.RunSummary, len(runs))
	for i := range runs {
		ids[i] = runs[i].ID
		index[runs[i].ID] = &runs[i]
	}
	crows, err := s.pool.Query(ctx, `
		SELECT run_id, kind, COUNT(*) FROM reconciliation_items
		WHERE run_id = ANY($1) AND kind <> $2 GROUP BY 1, 2`, ids, app.KindMatched)
	if err != nil {
		return nil, fmt.Errorf("query run counts: %w", err)
	}
	defer crows.Close()
	for crows.Next() {
		var (
			id   int64
			kind string
			n    int
		)
		if err := crows.Scan(&id, &kind, &n); err != nil {
			return nil, fmt.Errorf("scan run counts: %w", err)
		}
		index[id].Counts[kind] = n
	}
	return runs, crows.Err()
}

// LatestRun devolve a execução mais recente do dia.
func (s *ReconciliationStore) LatestRun(ctx context.Context, date time.Time) (app.RunSummary, error) {
	runs, err := s.scanRuns(ctx, `SELECT `+runColumns+` FROM reconciliation_runs r WHERE r.settlement_date = $1
		ORDER BY r.id DESC LIMIT 1`, date)
	if err != nil {
		return app.RunSummary{}, err
	}
	if len(runs) == 0 {
		return app.RunSummary{}, app.ErrNotFound
	}
	return runs[0], nil
}

// ListRuns devolve as execuções mais recentes.
func (s *ReconciliationStore) ListRuns(ctx context.Context, limit int) ([]app.RunSummary, error) {
	return s.scanRuns(ctx, `SELECT `+runColumns+` FROM reconciliation_runs r ORDER BY r.id DESC LIMIT $1`, pageSize(limit))
}

// RunItems devolve a partida dobrada de uma execução, com o estado do caso
// de cada divergência.
func (s *ReconciliationStore) RunItems(ctx context.Context, runID int64) ([]app.RunItem, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM reconciliation_runs WHERE id = $1)`, runID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check run: %w", err)
	}
	if !exists {
		return nil, app.ErrNotFound
	}
	rows, err := s.pool.Query(ctx, `
		SELECT i.kind, i.transaction_id, i.statement, i.ledger, i.adjusted, i.delta_cents, i.detail, c.id, COALESCE(c.status, '')
		FROM reconciliation_items i
		JOIN reconciliation_runs r ON r.id = i.run_id
		LEFT JOIN divergence_cases c ON c.settlement_date = r.settlement_date AND c.transaction_id = i.transaction_id
			AND c.kind = i.kind
		WHERE i.run_id = $1 ORDER BY i.position`, runID)
	if err != nil {
		return nil, fmt.Errorf("query run items: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (app.RunItem, error) {
		var (
			it     app.RunItem
			stmt   []byte
			ledger []byte
		)
		if err := row.Scan(&it.Kind, &it.TransactionID, &stmt, &ledger, &it.Adjusted, &it.DeltaCents, &it.Detail,
			&it.CaseID, &it.CaseStatus); err != nil {
			return it, err
		}
		var err error
		if it.Statement, err = unmarshalSide[domain.StatementLine](stmt); err != nil {
			return it, err
		}
		it.Ledger, err = unmarshalSide[domain.LedgerEntry](ledger)
		return it, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan run items: %w", err)
	}
	return items, nil
}

func unmarshalSide[T any](raw []byte) (*T, error) {
	if raw == nil {
		return nil, nil
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("decode run item side: %w", err)
	}
	return &v, nil
}
