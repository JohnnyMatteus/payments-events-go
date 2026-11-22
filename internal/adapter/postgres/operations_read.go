package postgres

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JohnnyMatteus/payments-events-go/internal/app"
)

const (
	defaultPageSize = 50
	maxPageSize     = 200
)

// OperationsReader implementa app.OperationsReader.
type OperationsReader struct {
	pool *pgxpool.Pool
}

// NewOperationsReader cria o leitor do painel.
func NewOperationsReader(pool *pgxpool.Pool) *OperationsReader {
	return &OperationsReader{pool: pool}
}

func pageSize(limit int) int {
	if limit < 1 {
		return defaultPageSize
	}
	return min(limit, maxPageSize)
}

// encodeCursor e decodeCursor guardam a posição da paginação por chave
// (received_at, id), que não pula nem repete linhas quando chegam eventos novos.
func encodeCursor(t time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(t.UnixMicro(), 10) + "|" + id))
}

func decodeCursor(c string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: invalid cursor", app.ErrInvalidInput)
	}
	micro, id, ok := strings.Cut(string(raw), "|")
	n, convErr := strconv.ParseInt(micro, 10, 64)
	if !ok || convErr != nil {
		return time.Time{}, "", fmt.Errorf("%w: invalid cursor", app.ErrInvalidInput)
	}
	return time.UnixMicro(n).UTC(), id, nil
}

const eventColumns = `
	pe.event_id, pe.event_type, pe.outcome, pe.received_at, pe.deliveries,
	COALESCE(le.merchant_id, pe.payload #>> '{data,merchant_id}', ''),
	COALESCE(le.transaction_id, pe.payload #>> '{data,transaction_id}', ''),
	COALESCE(le.payment_id, pe.payload #>> '{data,payment_id}', ''),
	COALESCE(le.direction, ''),
	COALESCE(le.amount_cents, (pe.payload #>> '{data,amount}')::bigint, 0),
	COALESCE(le.currency, pe.payload #>> '{data,currency}', ''),
	COALESCE(le.occurred_at, (pe.payload ->> 'created_at')::timestamptz),
	COALESCE(ob.status, '')`

const eventFrom = `
	FROM processed_events pe
	LEFT JOIN ledger_entries le ON le.event_id = pe.event_id
	LEFT JOIN outbox_messages ob ON ob.message_id = pe.event_id`

func scanEvent(row pgx.Row) (app.EventSummary, error) {
	var e app.EventSummary
	err := row.Scan(&e.EventID, &e.EventType, &e.Outcome, &e.ReceivedAt, &e.Deliveries, &e.MerchantID,
		&e.TransactionID, &e.PaymentID, &e.Direction, &e.AmountCents, &e.Currency, &e.OccurredAt, &e.OutboxStatus)
	return e, err
}

// ListEvents devolve os eventos do mais novo para o mais antigo.
func (r *OperationsReader) ListEvents(ctx context.Context, f app.EventFilter) (app.Page[app.EventSummary], error) {
	limit := pageSize(f.Limit)
	var (
		where []string
		args  []any
	)
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+strconv.Itoa(len(args))))
	}
	if q := strings.TrimSpace(f.Search); q != "" {
		add(`(pe.event_id ILIKE ? OR le.transaction_id ILIKE ? OR le.payment_id ILIKE ?
			OR pe.payload #>> '{data,transaction_id}' ILIKE ? OR pe.payload #>> '{data,payment_id}' ILIKE ?
			OR le.merchant_id ILIKE ?)`, "%"+escapeLike(q)+"%")
	}
	if f.Type != "" {
		add("pe.event_type = ?", f.Type)
	}
	if f.Outcome != "" {
		add("pe.outcome = ?", f.Outcome)
	}
	if f.Cursor != "" {
		at, id, err := decodeCursor(f.Cursor)
		if err != nil {
			return app.Page[app.EventSummary]{}, err
		}
		args = append(args, at, id)
		n := len(args)
		where = append(where, fmt.Sprintf("(pe.received_at, pe.event_id) < ($%d, $%d)", n-1, n))
	}
	query := "SELECT " + eventColumns + eventFrom
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, limit+1)
	query += fmt.Sprintf(" ORDER BY pe.received_at DESC, pe.event_id DESC LIMIT $%d", len(args))

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return app.Page[app.EventSummary]{}, fmt.Errorf("query events: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (app.EventSummary, error) { return scanEvent(row) })
	if err != nil {
		return app.Page[app.EventSummary]{}, fmt.Errorf("scan events: %w", err)
	}
	page := app.Page[app.EventSummary]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]
		page.NextCursor = encodeCursor(last.ReceivedAt, last.EventID)
	}
	return page, nil
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// EventTrail monta o caminho webhook → ledger → outbox → consumidores.
func (r *OperationsReader) EventTrail(ctx context.Context, eventID string) (app.EventTrail, error) {
	var (
		trail   app.EventTrail
		payload []byte
	)
	event, err := scanEvent(r.pool.QueryRow(ctx, "SELECT "+eventColumns+eventFrom+" WHERE pe.event_id = $1", eventID))
	if errors.Is(err, pgx.ErrNoRows) {
		return trail, app.ErrNotFound
	}
	if err != nil {
		return trail, fmt.Errorf("query event: %w", err)
	}
	trail.Event = event
	if err := r.pool.QueryRow(ctx, "SELECT payload FROM processed_events WHERE event_id = $1", eventID).Scan(&payload); err != nil {
		return trail, fmt.Errorf("query event payload: %w", err)
	}
	trail.Payload = payload

	var l app.LedgerView
	err = r.pool.QueryRow(ctx, `
		SELECT id, transaction_id, payment_id, COALESCE(merchant_id, ''), direction, amount_cents, currency, kind,
			COALESCE(adjusts_transaction_id, ''), occurred_at, recorded_at
		FROM ledger_entries WHERE event_id = $1`, eventID).
		Scan(&l.ID, &l.TransactionID, &l.PaymentID, &l.MerchantID, &l.Direction, &l.AmountCents, &l.Currency,
			&l.Kind, &l.AdjustsTransactionID, &l.OccurredAt, &l.RecordedAt)
	switch {
	case err == nil:
		trail.Ledger = &l
	case !errors.Is(err, pgx.ErrNoRows):
		return trail, fmt.Errorf("query ledger entry: %w", err)
	}

	var o app.OutboxView
	err = r.pool.QueryRow(ctx, `
		SELECT id, message_id, routing_key, status, attempts, COALESCE(last_error, ''), created_at, published_at, next_attempt_at
		FROM outbox_messages WHERE message_id = $1`, eventID).
		Scan(&o.ID, &o.MessageID, &o.RoutingKey, &o.Status, &o.Attempts, &o.LastError, &o.CreatedAt, &o.PublishedAt, &o.NextAttemptAt)
	switch {
	case err == nil:
		trail.Outbox = &o
	case !errors.Is(err, pgx.ErrNoRows):
		return trail, fmt.Errorf("query outbox message: %w", err)
	}

	rows, err := r.pool.Query(ctx,
		`SELECT consumer, consumed_at FROM consumed_messages WHERE message_id = $1 ORDER BY consumed_at`, eventID)
	if err != nil {
		return trail, fmt.Errorf("query consumptions: %w", err)
	}
	trail.Consumers, err = pgx.CollectRows(rows, pgx.RowToStructByPos[app.ConsumptionView])
	if err != nil {
		return trail, fmt.Errorf("scan consumptions: %w", err)
	}

	dead, err := r.ListDeadLetters(ctx, "", eventID)
	if err != nil {
		return trail, err
	}
	trail.DeadLetters = dead
	return trail, nil
}

// MerchantBalances soma o ledger por lojista e compara com a projeção do
// consumidor, que enxerga o mesmo lançamento só depois de consumir a mensagem.
func (r *OperationsReader) MerchantBalances(ctx context.Context) ([]app.MerchantBalance, error) {
	rows, err := r.pool.Query(ctx, `
		WITH ledger AS (
			SELECT COALESCE(merchant_id, 'sem lojista') AS merchant_id, currency,
				COUNT(DISTINCT payment_id) AS payments,
				COALESCE(SUM(amount_cents) FILTER (WHERE kind = 'provider' AND direction = 'credit'), 0) AS captured,
				COALESCE(SUM(amount_cents) FILTER (WHERE kind = 'provider' AND direction = 'debit'), 0) AS refunded,
				COALESCE(SUM(CASE direction WHEN 'credit' THEN amount_cents ELSE -amount_cents END)
					FILTER (WHERE kind = 'adjustment'), 0) AS adjustments,
				MAX(recorded_at) AS last_entry_at
			FROM ledger_entries GROUP BY 1, 2
		), projected AS (
			SELECT COALESCE(l.merchant_id, 'sem lojista') AS merchant_id, pb.currency,
				SUM(pb.captured_cents - pb.refunded_cents) AS net
			FROM payment_balances pb
			JOIN (SELECT DISTINCT payment_id, merchant_id FROM ledger_entries) l USING (payment_id)
			GROUP BY 1, 2
		), pending AS (
			SELECT COALESCE(le.merchant_id, 'sem lojista') AS merchant_id, le.currency, COUNT(*) AS n
			FROM ledger_entries le
			WHERE NOT EXISTS (SELECT 1 FROM consumed_messages cm WHERE cm.message_id = le.event_id)
			GROUP BY 1, 2
		)
		SELECT l.merchant_id, l.currency, l.payments, l.captured, l.refunded, l.adjustments,
			l.captured - l.refunded + l.adjustments, COALESCE(p.net, 0), l.last_entry_at, COALESCE(n.n, 0)
		FROM ledger l
		LEFT JOIN projected p ON p.merchant_id = l.merchant_id AND p.currency = l.currency
		LEFT JOIN pending n ON n.merchant_id = l.merchant_id AND n.currency = l.currency
		ORDER BY l.merchant_id, l.currency`)
	if err != nil {
		return nil, fmt.Errorf("query merchant balances: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[app.MerchantBalance])
	if err != nil {
		return nil, fmt.Errorf("scan merchant balances: %w", err)
	}
	return out, nil
}

// PaymentBalances lista a projeção por pagamento, comparada ao ledger.
func (r *OperationsReader) PaymentBalances(ctx context.Context, merchantID, cursor string, limit int) (app.Page[app.PaymentBalance], error) {
	size := pageSize(limit)
	args := []any{merchantID, cursor, size + 1}
	rows, err := r.pool.Query(ctx, `
		SELECT pb.payment_id, COALESCE(l.merchant_id, ''), pb.currency, pb.captured_cents, pb.refunded_cents,
			COALESCE(l.net, 0), pb.updated_at
		FROM payment_balances pb
		LEFT JOIN (
			SELECT payment_id, MAX(merchant_id) AS merchant_id,
				SUM(CASE direction WHEN 'credit' THEN amount_cents ELSE -amount_cents END) AS net
			FROM ledger_entries GROUP BY payment_id
		) l USING (payment_id)
		WHERE ($1 = '' OR l.merchant_id = $1) AND ($2 = '' OR pb.payment_id > $2)
		ORDER BY pb.payment_id LIMIT $3`, args...)
	if err != nil {
		return app.Page[app.PaymentBalance]{}, fmt.Errorf("query payment balances: %w", err)
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[app.PaymentBalance])
	if err != nil {
		return app.Page[app.PaymentBalance]{}, fmt.Errorf("scan payment balances: %w", err)
	}
	page := app.Page[app.PaymentBalance]{Items: items}
	if len(items) > size {
		page.Items = items[:size]
		page.NextCursor = page.Items[size-1].PaymentID
	}
	return page, nil
}

// PipelineHealth resume o outbox, o consumidor e a DLQ.
func (r *OperationsReader) PipelineHealth(ctx context.Context, now time.Time) (app.PipelineHealth, error) {
	var (
		h                    app.PipelineHealth
		oldestPending        *time.Time
		oldestDead           *time.Time
		lastReconciliationAt *time.Time
	)
	err := r.pool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status = 'pending'),
			COUNT(*) FILTER (WHERE status = 'dead'),
			MIN(created_at) FILTER (WHERE status = 'pending'),
			COUNT(*) FILTER (WHERE status = 'published' AND published_at > $1::timestamptz - interval '1 minute'),
			COUNT(*) FILTER (WHERE status = 'published' AND published_at > $1::timestamptz - interval '1 hour')
		FROM outbox_messages`, now).
		Scan(&h.OutboxPending, &h.OutboxDead, &oldestPending, &h.PublishedLastMinute, &h.PublishedLastHour)
	if err != nil {
		return h, fmt.Errorf("query outbox health: %w", err)
	}
	if oldestPending != nil {
		h.OldestPendingAge = now.Sub(*oldestPending)
	}
	err = r.pool.QueryRow(ctx, `
		SELECT COUNT(*), MIN(first_failed_at) FROM dead_letters WHERE status = 'open'`).Scan(&h.DeadLettersOpen, &oldestDead)
	if err != nil {
		return h, fmt.Errorf("query dead letters health: %w", err)
	}
	h.DeadLettersOpen += h.OutboxDead
	if oldestDead != nil {
		h.OldestDeadLetterAge = now.Sub(*oldestDead)
	}
	if err := r.pool.QueryRow(ctx, `SELECT MAX(consumed_at) FROM consumed_messages`).Scan(&h.LastConsumedAt); err != nil {
		return h, fmt.Errorf("query consumer health: %w", err)
	}
	if err := r.pool.QueryRow(ctx, `SELECT MAX(finished_at) FROM reconciliation_runs`).Scan(&lastReconciliationAt); err != nil {
		return h, fmt.Errorf("query reconciliation health: %w", err)
	}
	h.LastReconciliationAt = lastReconciliationAt
	return h, nil
}

// ListDeadLetters junta a DLQ do broker (arquivada) e o outbox esgotado.
// status vazio traz todos; messageID filtra por mensagem.
func (r *OperationsReader) ListDeadLetters(ctx context.Context, status, messageID string) ([]app.DeadLetter, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT * FROM (
			SELECT dl.id, 'broker' AS source, dl.message_id, dl.routing_key, dl.payload, dl.reason, dl.error,
				dl.deliveries, dl.first_failed_at, dl.status, COALESCE(dl.resolved_by, ''), dl.resolved_at,
				COALESCE(dl.resolution_note, ''),
				EXISTS (SELECT 1 FROM outbox_messages ob WHERE ob.message_id = dl.message_id) AS has_outbox
			FROM dead_letters dl
			UNION ALL
			SELECT ob.id, 'outbox', ob.message_id, ob.routing_key, convert_to(ob.payload::text, 'UTF8'),
				'outbox_exhausted', COALESCE(ob.last_error, ''), ob.attempts, ob.created_at,
				CASE ob.status WHEN 'dead' THEN 'open' ELSE ob.status END,
				COALESCE(ob.resolved_by, ''), ob.resolved_at, COALESCE(ob.resolution_note, ''), true
			FROM outbox_messages ob WHERE ob.status IN ('dead', 'discarded')
		) d
		WHERE ($1 = '' OR d.status = $1) AND ($2 = '' OR d.message_id = $2)
		ORDER BY d.first_failed_at, d.id`, status, messageID)
	if err != nil {
		return nil, fmt.Errorf("query dead letters: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (app.DeadLetter, error) {
		var (
			d      app.DeadLetter
			source string
		)
		err := row.Scan(&d.ID, &source, &d.MessageID, &d.RoutingKey, &d.Payload, &d.Reason, &d.Error, &d.Deliveries,
			&d.FirstFailedAt, &d.Status, &d.ResolvedBy, &d.ResolvedAt, &d.ResolutionNote, &d.HasOutbox)
		d.Source = app.DeadLetterSource(source)
		return d, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan dead letters: %w", err)
	}
	return out, nil
}
