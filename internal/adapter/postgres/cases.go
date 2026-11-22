package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JohnnyMatteus/payments-events-go/internal/app"
	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
)

// CaseRepository implementa app.CaseRepository.
type CaseRepository struct {
	pool *pgxpool.Pool
}

// NewCaseRepository cria o repositório de casos e ajustes.
func NewCaseRepository(pool *pgxpool.Pool) *CaseRepository {
	return &CaseRepository{pool: pool}
}

const caseColumns = `id, settlement_date, transaction_id, kind, status, statement, ledger, delta_cents, detail,
	COALESCE(explanation, ''), COALESCE(resolved_by, ''), resolved_at, updated_at`

func scanCase(row pgx.Row) (app.Case, error) {
	var (
		c          app.Case
		kind, stat string
		stmt, led  []byte
	)
	err := row.Scan(&c.ID, &c.Date, &c.TransactionID, &kind, &stat, &stmt, &led, &c.DeltaCents, &c.Detail,
		&c.Explanation, &c.ResolvedBy, &c.ResolvedAt, &c.UpdatedAt)
	if err != nil {
		return c, err
	}
	c.Date = c.Date.UTC()
	c.Kind, c.Status = domain.DivergenceKind(kind), domain.CaseStatus(stat)
	c.Divergence = domain.Divergence{Kind: c.Kind, TransactionID: c.TransactionID, Detail: c.Detail}
	if c.Divergence.Statement, err = unmarshalSide[domain.StatementLine](stmt); err != nil {
		return c, err
	}
	c.Divergence.Ledger, err = unmarshalSide[domain.LedgerEntry](led)
	return c, err
}

const adjustmentColumns = `id, case_id, transaction_id, COALESCE(adjusts_transaction_id, ''), COALESCE(merchant_id, ''),
	payment_id, direction, amount_cents, currency, occurred_at, reason, status, proposed_by, COALESCE(proposed_by_id, ''), proposed_at,
	COALESCE(decided_by, ''), decided_at, COALESCE(decision_note, ''), COALESCE(event_id, '')`

func scanAdjustment(row pgx.Row) (app.Adjustment, error) {
	var (
		a           app.Adjustment
		direction   string
		currency    string
		status      string
		transaction string
	)
	err := row.Scan(&a.ID, &a.CaseID, &transaction, &a.Entry.AdjustsTransactionID, &a.Entry.MerchantID,
		&a.Entry.PaymentID, &direction, &a.Entry.AmountCents, &currency, &a.Entry.OccurredAt, &a.Reason, &status,
		&a.ProposedBy, &a.ProposedByID, &a.ProposedAt, &a.DecidedBy, &a.DecidedAt, &a.DecisionNote, &a.EventID)
	a.Entry.TransactionID = transaction
	a.Entry.Direction, a.Entry.Currency = domain.Direction(direction), domain.Currency(currency)
	a.Entry.OccurredAt = a.Entry.OccurredAt.UTC()
	a.Status = domain.AdjustmentStatus(status)
	return a, err
}

func queryAdjustments(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, where string, args ...any) ([]app.Adjustment, error) {
	rows, err := q.Query(ctx, "SELECT "+adjustmentColumns+" FROM ledger_adjustments WHERE "+where+" ORDER BY id", args...)
	if err != nil {
		return nil, fmt.Errorf("query adjustments: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (app.Adjustment, error) { return scanAdjustment(row) })
	if err != nil {
		return nil, fmt.Errorf("scan adjustments: %w", err)
	}
	return out, nil
}

// GetCase devolve o caso com o histórico de ajustes.
func (r *CaseRepository) GetCase(ctx context.Context, id int64) (app.Case, error) {
	c, err := scanCase(r.pool.QueryRow(ctx, "SELECT "+caseColumns+" FROM divergence_cases WHERE id = $1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return c, app.ErrNotFound
	}
	if err != nil {
		return c, fmt.Errorf("query case: %w", err)
	}
	c.Adjustments, err = queryAdjustments(ctx, r.pool, "case_id = $1", id)
	return c, err
}

// ListCases lista os casos, os mais urgentes primeiro.
func (r *CaseRepository) ListCases(ctx context.Context, f app.CaseFilter) ([]app.Case, error) {
	var date *time.Time
	if f.Date != nil {
		date = f.Date
	}
	rows, err := r.pool.Query(ctx, `
		SELECT `+caseColumns+` FROM divergence_cases
		WHERE ($1 = '' OR status = $1) AND ($2::date IS NULL OR settlement_date = $2::date)
		ORDER BY settlement_date DESC, id`, f.Status, date)
	if err != nil {
		return nil, fmt.Errorf("query cases: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (app.Case, error) { return scanCase(row) })
	if err != nil {
		return nil, fmt.Errorf("scan cases: %w", err)
	}
	return out, nil
}

// PendingAdjustments lista os ajustes que esperam aprovação.
func (r *CaseRepository) PendingAdjustments(ctx context.Context) ([]app.Adjustment, error) {
	return queryAdjustments(ctx, r.pool, "status = 'proposed'")
}

func lockCase(ctx context.Context, tx pgx.Tx, id int64) (app.Case, error) {
	c, err := scanCase(tx.QueryRow(ctx, "SELECT "+caseColumns+" FROM divergence_cases WHERE id = $1 FOR UPDATE", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return c, app.ErrNotFound
	}
	if err != nil {
		return c, fmt.Errorf("lock case: %w", err)
	}
	return c, nil
}

// Explain encerra o caso com a justificativa.
func (r *CaseRepository) Explain(ctx context.Context, id int64, decide func(app.Case) error, operator, reason string) error {
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		c, err := lockCase(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := decide(c); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			UPDATE divergence_cases SET status = 'explained', explanation = $2, resolved_by = $3, resolved_at = now(),
				updated_at = now()
			WHERE id = $1`, id, reason, operator)
		if err != nil {
			return fmt.Errorf("explain case: %w", err)
		}
		return nil
	})
}

// ProposeAdjustment grava o ajuste proposto e marca o caso.
func (r *CaseRepository) ProposeAdjustment(ctx context.Context, caseID int64, build func(app.Case) (domain.LedgerEntry, error),
	actor app.Actor, reason string) (app.Adjustment, error) {
	var out app.Adjustment
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		c, err := lockCase(ctx, tx, caseID)
		if err != nil {
			return err
		}
		entry, err := build(c)
		if err != nil {
			return err
		}
		out, err = scanAdjustment(tx.QueryRow(ctx, `
			INSERT INTO ledger_adjustments (case_id, transaction_id, adjusts_transaction_id, merchant_id, payment_id,
				direction, amount_cents, currency, occurred_at, reason, proposed_by, proposed_by_id)
			VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), $5, $6, $7, $8, $9, $10, $11, $12)
			RETURNING `+adjustmentColumns,
			caseID, entry.TransactionID, entry.AdjustsTransactionID, entry.MerchantID, entry.PaymentID,
			string(entry.Direction), entry.AmountCents, string(entry.Currency), entry.OccurredAt, reason, actor.Name, actor.ID))
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: the case already has a pending adjustment", app.ErrConflict)
		}
		if err != nil {
			return fmt.Errorf("insert adjustment: %w", err)
		}
		_, err = tx.Exec(ctx, `UPDATE divergence_cases SET status = 'adjustment_proposed', updated_at = now() WHERE id = $1`, caseID)
		if err != nil {
			return fmt.Errorf("mark case: %w", err)
		}
		return nil
	})
	return out, err
}

func lockAdjustment(ctx context.Context, tx pgx.Tx, id int64) (app.Adjustment, error) {
	a, err := scanAdjustment(tx.QueryRow(ctx,
		"SELECT "+adjustmentColumns+" FROM ledger_adjustments WHERE id = $1 FOR UPDATE", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return a, app.ErrNotFound
	}
	if err != nil {
		return a, fmt.Errorf("lock adjustment: %w", err)
	}
	return a, nil
}

// ApproveAdjustment grava o lançamento e a mensagem de outbox na mesma
// transação que aprova o ajuste e resolve o caso.
func (r *CaseRepository) ApproveAdjustment(ctx context.Context, id int64, decide func(app.Adjustment) (app.Recording, error),
	actor app.Actor, note string) (app.Adjustment, error) {
	var out app.Adjustment
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		a, err := lockAdjustment(ctx, tx, id)
		if err != nil {
			return err
		}
		rec, err := decide(a)
		if err != nil {
			return err
		}
		outcome, err := record(ctx, tx, rec, rec.RawPayload)
		if err != nil {
			return err
		}
		if outcome != app.OutcomeRecorded {
			return fmt.Errorf("%w: ledger already has transaction %s", app.ErrConflict, rec.Entry.TransactionID)
		}
		out, err = scanAdjustment(tx.QueryRow(ctx, `
			UPDATE ledger_adjustments SET status = 'approved', decided_by = $2, decided_by_id = $5, decided_at = now(),
				decision_note = NULLIF($3, ''), event_id = $4
			WHERE id = $1 RETURNING `+adjustmentColumns, id, actor.Name, note, rec.Event.ID, actor.ID))
		if err != nil {
			return fmt.Errorf("approve adjustment: %w", err)
		}
		_, err = tx.Exec(ctx, `
			UPDATE divergence_cases SET status = 'resolved', resolved_by = $2, resolved_at = now(), updated_at = now()
			WHERE id = $1`, a.CaseID, actor.Name)
		if err != nil {
			return fmt.Errorf("resolve case: %w", err)
		}
		return nil
	})
	return out, err
}

// RejectAdjustment recusa o ajuste e reabre o caso.
func (r *CaseRepository) RejectAdjustment(ctx context.Context, id int64, decide func(app.Adjustment) error,
	actor app.Actor, note string) (app.Adjustment, error) {
	var out app.Adjustment
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		a, err := lockAdjustment(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := decide(a); err != nil {
			return err
		}
		out, err = scanAdjustment(tx.QueryRow(ctx, `
			UPDATE ledger_adjustments SET status = 'rejected', decided_by = $2, decided_by_id = $4, decided_at = now(), decision_note = $3
			WHERE id = $1 RETURNING `+adjustmentColumns, id, actor.Name, note, actor.ID))
		if err != nil {
			return fmt.Errorf("reject adjustment: %w", err)
		}
		_, err = tx.Exec(ctx, `UPDATE divergence_cases SET status = 'open', updated_at = now() WHERE id = $1`, a.CaseID)
		if err != nil {
			return fmt.Errorf("reopen case: %w", err)
		}
		return nil
	})
	return out, err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
