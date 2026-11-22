package httpapi

import (
	"encoding/json"
	"time"

	"github.com/JohnnyMatteus/payments-events-go/internal/app"
	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
)

// Os tipos abaixo são o contrato JSON da API de operação. Valores monetários
// sempre em centavos inteiros com a moeda ao lado; o painel formata.

type pageResponse[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

func pageDTO[S, T any](p app.Page[S], conv func(S) T) pageResponse[T] {
	return pageResponse[T]{Items: mapSlice(p.Items, conv), NextCursor: p.NextCursor}
}

// mapSlice devolve sempre uma lista (nunca null) para o painel.
func mapSlice[S, T any](in []S, conv func(S) T) []T {
	out := make([]T, 0, len(in))
	for _, v := range in {
		out = append(out, conv(v))
	}
	return out
}

type eventDTO struct {
	EventID       string     `json:"event_id"`
	EventType     string     `json:"event_type"`
	Outcome       string     `json:"outcome"`
	MerchantID    string     `json:"merchant_id"`
	TransactionID string     `json:"transaction_id"`
	PaymentID     string     `json:"payment_id"`
	Direction     string     `json:"direction"`
	AmountCents   int64      `json:"amount_cents"`
	Currency      string     `json:"currency"`
	OccurredAt    *time.Time `json:"occurred_at"`
	ReceivedAt    time.Time  `json:"received_at"`
	OutboxStatus  string     `json:"outbox_status"`
	Deliveries    int        `json:"deliveries"`
}

func eventDTOOf(e app.EventSummary) eventDTO {
	return eventDTO(e)
}

type ledgerDTO struct {
	ID                   int64     `json:"id"`
	TransactionID        string    `json:"transaction_id"`
	PaymentID            string    `json:"payment_id"`
	MerchantID           string    `json:"merchant_id"`
	Direction            string    `json:"direction"`
	AmountCents          int64     `json:"amount_cents"`
	Currency             string    `json:"currency"`
	Kind                 string    `json:"kind"`
	AdjustsTransactionID string    `json:"adjusts_transaction_id,omitempty"`
	OccurredAt           time.Time `json:"occurred_at"`
	RecordedAt           time.Time `json:"recorded_at"`
}

func ledgerDTOOf(l app.LedgerView) ledgerDTO { return ledgerDTO(l) }

type outboxDTO struct {
	ID            int64      `json:"id"`
	MessageID     string     `json:"message_id"`
	RoutingKey    string     `json:"routing_key"`
	Status        string     `json:"status"`
	Attempts      int        `json:"attempts"`
	LastError     string     `json:"last_error,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	PublishedAt   *time.Time `json:"published_at"`
	NextAttemptAt time.Time  `json:"next_attempt_at"`
	Archived      bool       `json:"archived"`
}

type consumptionDTO struct {
	Consumer   string    `json:"consumer"`
	ConsumedAt time.Time `json:"consumed_at"`
}

type trailDTO struct {
	Event       eventDTO         `json:"event"`
	Payload     json.RawMessage  `json:"payload"`
	Ledger      *ledgerDTO       `json:"ledger"`
	Outbox      *outboxDTO       `json:"outbox"`
	Consumers   []consumptionDTO `json:"consumers"`
	DeadLetters []deadLetterDTO  `json:"dead_letters"`
}

func trailDTOOf(t app.EventTrail, now time.Time) trailDTO {
	out := trailDTO{
		Event:   eventDTOOf(t.Event),
		Payload: t.Payload,
		Consumers: mapSlice(t.Consumers, func(c app.ConsumptionView) consumptionDTO {
			return consumptionDTO(c)
		}),
		DeadLetters: mapSlice(t.DeadLetters, func(d app.DeadLetter) deadLetterDTO { return deadLetterDTOOf(d, now) }),
	}
	if t.Ledger != nil {
		l := ledgerDTO(*t.Ledger)
		out.Ledger = &l
	}
	if t.Outbox != nil {
		o := outboxDTO(*t.Outbox)
		out.Outbox = &o
	}
	return out
}

type merchantBalanceDTO struct {
	MerchantID          string     `json:"merchant_id"`
	Currency            string     `json:"currency"`
	Payments            int        `json:"payments"`
	CapturedCents       int64      `json:"captured_cents"`
	RefundedCents       int64      `json:"refunded_cents"`
	AdjustmentCents     int64      `json:"adjustment_cents"`
	LedgerNetCents      int64      `json:"ledger_net_cents"`
	ProjectedNetCents   int64      `json:"projected_net_cents"`
	LastEntryAt         *time.Time `json:"last_entry_at"`
	UnprojectedMessages int        `json:"unprojected_messages"`
}

func merchantBalanceDTOOf(b app.MerchantBalance) merchantBalanceDTO { return merchantBalanceDTO(b) }

type paymentBalanceDTO struct {
	PaymentID      string    `json:"payment_id"`
	MerchantID     string    `json:"merchant_id"`
	Currency       string    `json:"currency"`
	CapturedCents  int64     `json:"captured_cents"`
	RefundedCents  int64     `json:"refunded_cents"`
	LedgerNetCents int64     `json:"ledger_net_cents"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func paymentBalanceDTOOf(b app.PaymentBalance) paymentBalanceDTO { return paymentBalanceDTO(b) }

type pipelineDTO struct {
	OutboxPending              int        `json:"outbox_pending"`
	OutboxDead                 int        `json:"outbox_dead"`
	OldestPendingAgeSeconds    int64      `json:"oldest_pending_age_seconds"`
	PublishedLastMinute        int        `json:"published_last_minute"`
	PublishedLastHour          int        `json:"published_last_hour"`
	DeadLettersOpen            int        `json:"dead_letters_open"`
	OldestDeadLetterAgeSeconds int64      `json:"oldest_dead_letter_age_seconds"`
	LastConsumedAt             *time.Time `json:"last_consumed_at"`
	LastReconciliationAt       *time.Time `json:"last_reconciliation_at"`
}

func pipelineDTOOf(h app.PipelineHealth) pipelineDTO {
	return pipelineDTO{
		OutboxPending: h.OutboxPending, OutboxDead: h.OutboxDead,
		OldestPendingAgeSeconds: int64(h.OldestPendingAge.Seconds()),
		PublishedLastMinute:     h.PublishedLastMinute, PublishedLastHour: h.PublishedLastHour,
		DeadLettersOpen:            h.DeadLettersOpen,
		OldestDeadLetterAgeSeconds: int64(h.OldestDeadLetterAge.Seconds()),
		LastConsumedAt:             h.LastConsumedAt, LastReconciliationAt: h.LastReconciliationAt,
	}
}

type deadLetterDTO struct {
	ID             int64      `json:"id"`
	Source         string     `json:"source"`
	MessageID      string     `json:"message_id"`
	RoutingKey     string     `json:"routing_key"`
	Payload        string     `json:"payload"`
	Reason         string     `json:"reason"`
	Error          string     `json:"error"`
	Deliveries     int        `json:"deliveries"`
	FirstFailedAt  time.Time  `json:"first_failed_at"`
	AgeSeconds     int64      `json:"age_seconds"`
	Status         string     `json:"status"`
	ResolvedBy     string     `json:"resolved_by,omitempty"`
	ResolvedAt     *time.Time `json:"resolved_at"`
	ResolutionNote string     `json:"resolution_note,omitempty"`
	CanReprocess   bool       `json:"can_reprocess"`
}

func deadLetterDTOOf(d app.DeadLetter, now time.Time) deadLetterDTO {
	return deadLetterDTO{
		ID: d.ID, Source: string(d.Source), MessageID: d.MessageID, RoutingKey: d.RoutingKey,
		Payload: string(d.Payload), Reason: d.Reason, Error: d.Error, Deliveries: d.Deliveries,
		FirstFailedAt: d.FirstFailedAt, AgeSeconds: int64(now.Sub(d.FirstFailedAt).Seconds()), Status: d.Status,
		ResolvedBy: d.ResolvedBy, ResolvedAt: d.ResolvedAt, ResolutionNote: d.ResolutionNote,
		CanReprocess: d.Status == "open" && d.HasOutbox,
	}
}

type statementDTO struct {
	Date         string    `json:"date"`
	Ref          string    `json:"ref"`
	Size         int64     `json:"size_bytes"`
	SHA256       string    `json:"sha256"`
	LastModified time.Time `json:"last_modified"`
}

func statementDTOOf(s app.StatementObject) statementDTO {
	return statementDTO{
		Date: s.Date.Format(time.DateOnly), Ref: s.Ref, Size: s.Size, SHA256: s.SHA256, LastModified: s.LastModified,
	}
}

type runDTO struct {
	ID             int64          `json:"id"`
	Date           string         `json:"date"`
	StatementRef   string         `json:"statement_ref"`
	Trigger        string         `json:"trigger"`
	TriggeredBy    string         `json:"triggered_by"`
	Status         string         `json:"status"`
	Error          string         `json:"error,omitempty"`
	StatementLines int            `json:"statement_lines"`
	LedgerEntries  int            `json:"ledger_entries"`
	Matched        int            `json:"matched"`
	Reversed       int            `json:"reversed"`
	StatementNet   int64          `json:"statement_net_cents"`
	LedgerNet      int64          `json:"ledger_net_cents"`
	Counts         map[string]int `json:"divergences"`
	StartedAt      time.Time      `json:"started_at"`
	FinishedAt     time.Time      `json:"finished_at"`
}

func runDTOOf(r app.RunSummary) runDTO {
	counts := r.Counts
	if counts == nil {
		counts = map[string]int{}
	}
	return runDTO{
		ID: r.ID, Date: r.Date.Format(time.DateOnly), StatementRef: r.StatementRef, Trigger: string(r.Trigger),
		TriggeredBy: r.TriggeredBy, Status: r.Status, Error: r.Error, StatementLines: r.StatementLines,
		LedgerEntries: r.LedgerEntries, Matched: r.Matched, Reversed: r.Reversed, StatementNet: r.StatementNet,
		LedgerNet: r.LedgerNet, Counts: counts, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
	}
}

type statementLineDTO struct {
	LineNumber    int       `json:"line_number"`
	TransactionID string    `json:"transaction_id"`
	PaymentID     string    `json:"payment_id"`
	Direction     string    `json:"direction"`
	AmountCents   int64     `json:"amount_cents"`
	Currency      string    `json:"currency"`
	OccurredAt    time.Time `json:"occurred_at"`
}

type ledgerSideDTO struct {
	TransactionID string    `json:"transaction_id"`
	PaymentID     string    `json:"payment_id"`
	MerchantID    string    `json:"merchant_id,omitempty"`
	Direction     string    `json:"direction"`
	AmountCents   int64     `json:"amount_cents"`
	Currency      string    `json:"currency"`
	OccurredAt    time.Time `json:"occurred_at"`
	EventID       string    `json:"event_id,omitempty"`
}

type runItemDTO struct {
	Kind          string            `json:"kind"`
	TransactionID string            `json:"transaction_id"`
	Statement     *statementLineDTO `json:"statement"`
	Ledger        *ledgerSideDTO    `json:"ledger"`
	Adjusted      bool              `json:"adjusted"`
	DeltaCents    int64             `json:"delta_cents"`
	Detail        string            `json:"detail,omitempty"`
	CaseID        *int64            `json:"case_id"`
	CaseStatus    string            `json:"case_status,omitempty"`
}

func statementSide(l *domain.StatementLine) *statementLineDTO {
	if l == nil {
		return nil
	}
	return &statementLineDTO{
		LineNumber: l.LineNumber, TransactionID: l.TransactionID, PaymentID: l.PaymentID, Direction: string(l.Direction),
		AmountCents: l.AmountCents, Currency: string(l.Currency), OccurredAt: l.OccurredAt,
	}
}

func ledgerSide(l *domain.LedgerEntry) *ledgerSideDTO {
	if l == nil {
		return nil
	}
	return &ledgerSideDTO{
		TransactionID: l.TransactionID, PaymentID: l.PaymentID, MerchantID: l.MerchantID, Direction: string(l.Direction),
		AmountCents: l.AmountCents, Currency: string(l.Currency), OccurredAt: l.OccurredAt, EventID: l.EventID,
	}
}

func runItemDTOOf(i app.RunItem) runItemDTO {
	return runItemDTO{
		Kind: i.Kind, TransactionID: i.TransactionID, Statement: statementSide(i.Statement), Ledger: ledgerSide(i.Ledger),
		Adjusted: i.Adjusted, DeltaCents: i.DeltaCents, Detail: i.Detail, CaseID: i.CaseID, CaseStatus: i.CaseStatus,
	}
}

type adjustmentDTO struct {
	ID            int64          `json:"id"`
	CaseID        int64          `json:"case_id"`
	Entry         *ledgerSideDTO `json:"entry"`
	AdjustsTx     string         `json:"adjusts_transaction_id,omitempty"`
	Reason        string         `json:"reason"`
	Status        string         `json:"status"`
	ProposedBy    string         `json:"proposed_by"`
	ProposedAt    time.Time      `json:"proposed_at"`
	DecidedBy     string         `json:"decided_by,omitempty"`
	DecidedAt     *time.Time     `json:"decided_at"`
	DecisionNote  string         `json:"decision_note,omitempty"`
	LedgerEventID string         `json:"event_id,omitempty"`
}

func adjustmentDTOOf(a app.Adjustment) adjustmentDTO {
	return adjustmentDTO{
		ID: a.ID, CaseID: a.CaseID, Entry: ledgerSide(&a.Entry), AdjustsTx: a.Entry.AdjustsTransactionID,
		Reason: a.Reason, Status: string(a.Status), ProposedBy: a.ProposedBy, ProposedAt: a.ProposedAt,
		DecidedBy: a.DecidedBy, DecidedAt: a.DecidedAt, DecisionNote: a.DecisionNote, LedgerEventID: a.EventID,
	}
}

type caseDTO struct {
	ID            int64             `json:"id"`
	Date          string            `json:"date"`
	TransactionID string            `json:"transaction_id"`
	Kind          string            `json:"kind"`
	Status        string            `json:"status"`
	Statement     *statementLineDTO `json:"statement"`
	Ledger        *ledgerSideDTO    `json:"ledger"`
	DeltaCents    int64             `json:"delta_cents"`
	Detail        string            `json:"detail,omitempty"`
	Explanation   string            `json:"explanation,omitempty"`
	ResolvedBy    string            `json:"resolved_by,omitempty"`
	ResolvedAt    *time.Time        `json:"resolved_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
	Adjustments   []adjustmentDTO   `json:"adjustments"`
}

func caseDTOOf(c app.Case) caseDTO {
	return caseDTO{
		ID: c.ID, Date: c.Date.Format(time.DateOnly), TransactionID: c.TransactionID, Kind: string(c.Kind),
		Status: string(c.Status), Statement: statementSide(c.Divergence.Statement), Ledger: ledgerSide(c.Divergence.Ledger),
		DeltaCents: c.DeltaCents, Detail: c.Detail, Explanation: c.Explanation, ResolvedBy: c.ResolvedBy,
		ResolvedAt: c.ResolvedAt, UpdatedAt: c.UpdatedAt, Adjustments: mapSlice(c.Adjustments, adjustmentDTOOf),
	}
}
