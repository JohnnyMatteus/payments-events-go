package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JohnnyMatteus/payments-events-go/internal/app"
)

// retentionLockID serializa execuções concorrentes do job de retenção (e a
// criação de partições).
const retentionLockID = 7_316_420_002

// Retention implementa app.RetentionRepository e app.LedgerArchiveRepository.
type Retention struct {
	pool *pgxpool.Pool
}

var (
	_ app.RetentionRepository     = (*Retention)(nil)
	_ app.LedgerArchiveRepository = (*Retention)(nil)
)

// NewRetention cria o repositório de retenção.
func NewRetention(pool *pgxpool.Pool) *Retention { return &Retention{pool: pool} }

// PurgePublishedOutbox apaga mensagens já publicadas. As demais ficam.
func (r *Retention) PurgePublishedOutbox(ctx context.Context, before time.Time, limit int) (int, error) {
	tag, err := r.pool.Exec(ctx, `
		WITH doomed AS (
			SELECT id FROM outbox_messages
			WHERE status = 'published' AND published_at < $1
			ORDER BY id LIMIT $2
			FOR UPDATE SKIP LOCKED)
		DELETE FROM outbox_messages o USING doomed d WHERE o.id = d.id`, before, limit)
	if err != nil {
		return 0, fmt.Errorf("purge published outbox: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// summarizedPayload é o que as telas leem de processed_events.payload quando o
// evento não tem lançamento (ver eventColumns em operations_read.go).
const summarizedPayload = `jsonb_strip_nulls(jsonb_build_object(
	'created_at', payload -> 'created_at',
	'data', jsonb_build_object(
		'merchant_id', payload #> '{data,merchant_id}',
		'transaction_id', payload #> '{data,transaction_id}',
		'payment_id', payload #> '{data,payment_id}',
		'amount', payload #> '{data,amount}',
		'currency', payload #> '{data,currency}')))`

// ArchiveEventPayloads copia o payload para processed_events_archive e reduz o
// da linha a um resumo, na mesma transação. A linha, com o event_id que
// garante a idempotência, nunca sai de processed_events.
func (r *Retention) ArchiveEventPayloads(ctx context.Context, before time.Time, limit int) (int, error) {
	var archived int
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", retentionLockID); err != nil {
			return fmt.Errorf("lock retention: %w", err)
		}
		rows, err := tx.Query(ctx, `
			SELECT event_id, received_at FROM processed_events
			WHERE archived_at IS NULL AND received_at < $1
			ORDER BY received_at, event_id LIMIT $2
			FOR UPDATE SKIP LOCKED`, before, limit)
		if err != nil {
			return fmt.Errorf("select events to archive: %w", err)
		}
		var ids []string
		months := map[time.Time]bool{}
		for rows.Next() {
			var (
				id string
				at time.Time
			)
			if err := rows.Scan(&id, &at); err != nil {
				rows.Close()
				return fmt.Errorf("scan event to archive: %w", err)
			}
			ids = append(ids, id)
			months[monthStart(at)] = true
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("read events to archive: %w", err)
		}
		if len(ids) == 0 {
			return nil
		}
		for m := range months {
			if err := ensureArchivePartition(ctx, tx, m); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO processed_events_archive (event_id, event_type, outcome, payload, received_at, deliveries)
			SELECT event_id, event_type, outcome, payload, received_at, deliveries
			FROM processed_events WHERE event_id = ANY($1)
			ON CONFLICT DO NOTHING`, ids); err != nil {
			return fmt.Errorf("copy payloads to archive: %w", err)
		}
		// Só reduz a linha se o arquivo tem todas as cópias; senão desfaz tudo.
		var copied int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM processed_events p
			JOIN processed_events_archive a ON a.event_id = p.event_id AND a.received_at = p.received_at
			WHERE p.event_id = ANY($1)`, ids).Scan(&copied); err != nil {
			return fmt.Errorf("verify archive copies: %w", err)
		}
		if copied != len(ids) {
			return fmt.Errorf("archive holds %d of %d payloads, nothing was changed", copied, len(ids))
		}
		tag, err := tx.Exec(ctx, `
			UPDATE processed_events SET payload = `+summarizedPayload+`, archived_at = now()
			WHERE event_id = ANY($1) AND archived_at IS NULL`, ids)
		if err != nil {
			return fmt.Errorf("shrink archived events: %w", err)
		}
		archived = int(tag.RowsAffected())
		return nil
	})
	return archived, err
}

func monthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// ensureArchivePartition cria a partição mensal (UTC). Nome e limites saem de
// um time.Time, nunca de texto externo.
func ensureArchivePartition(ctx context.Context, tx pgx.Tx, month time.Time) error {
	name := pgx.Identifier{"processed_events_archive_" + month.Format("2006_01")}.Sanitize()
	from, to := month.Format("2006-01-02 15:04:05+00"), month.AddDate(0, 1, 0).Format("2006-01-02 15:04:05+00")
	ddl := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s PARTITION OF processed_events_archive FOR VALUES FROM ('%s') TO ('%s')`,
		name, from, to)
	if _, err := tx.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("create archive partition %s: %w", name, err)
	}
	return nil
}

// PendingLedgerMonths lista os meses com lançamentos sem exportação registrada.
func (r *Retention) PendingLedgerMonths(ctx context.Context, before time.Time) ([]time.Time, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT date_trunc('month', le.recorded_at AT TIME ZONE 'UTC')::date AS month
		FROM ledger_entries le
		WHERE le.recorded_at < $1
		  AND NOT EXISTS (SELECT 1 FROM ledger_archives a
		                  WHERE a.month = date_trunc('month', le.recorded_at AT TIME ZONE 'UTC')::date)
		ORDER BY month`, before)
	if err != nil {
		return nil, fmt.Errorf("list ledger months: %w", err)
	}
	months, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (time.Time, error) {
		var m time.Time
		err := row.Scan(&m)
		return monthStart(m), err
	})
	if err != nil {
		return nil, fmt.Errorf("scan ledger months: %w", err)
	}
	return months, nil
}

// LedgerMonthRows devolve os lançamentos gravados no mês (UTC), por id.
func (r *Retention) LedgerMonthRows(ctx context.Context, month time.Time) ([]app.LedgerArchiveRow, error) {
	month = monthStart(month)
	rows, err := r.pool.Query(ctx, `
		SELECT id, event_id, COALESCE(merchant_id, ''), transaction_id, payment_id, direction, amount_cents,
			currency, occurred_at, recorded_at, kind, COALESCE(adjusts_transaction_id, '')
		FROM ledger_entries
		WHERE recorded_at >= $1 AND recorded_at < $2
		ORDER BY id`, month, month.AddDate(0, 1, 0))
	if err != nil {
		return nil, fmt.Errorf("query ledger month: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (app.LedgerArchiveRow, error) {
		var e app.LedgerArchiveRow
		err := row.Scan(&e.ID, &e.EventID, &e.MerchantID, &e.TransactionID, &e.PaymentID, &e.Direction, &e.AmountCents,
			&e.Currency, &e.OccurredAt, &e.RecordedAt, &e.Kind, &e.AdjustsTransactionID)
		return e, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan ledger month: %w", err)
	}
	return out, nil
}

// RecordLedgerArchive registra a exportação; false se o mês já constava.
func (r *Retention) RecordLedgerArchive(ctx context.Context, a app.LedgerArchive) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		INSERT INTO ledger_archives (month, object_ref, row_count, sha256) VALUES ($1, $2, $3, $4)
		ON CONFLICT (month) DO NOTHING`, a.Month, a.ObjectRef, a.RowCount, a.SHA256)
	if err != nil {
		return false, fmt.Errorf("record ledger archive: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
