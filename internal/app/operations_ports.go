package app

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
)

// Erros das operações do painel, traduzidos pelo adaptador HTTP.
var (
	ErrNotFound         = errors.New("not found")
	ErrInvalidInput     = errors.New("invalid input")
	ErrConflict         = errors.New("conflict with current state")
	ErrNoOutboxOrigin   = errors.New("dead letter has no outbox message to replay")
	ErrStatementMissing = errors.New("statement not found in bucket")
)

// Page é uma página de resultados com cursor opaco para a próxima.
type Page[T any] struct {
	Items      []T
	NextCursor string
}

// EventFilter filtra a lista de eventos recebidos.
type EventFilter struct {
	Search  string
	Type    string
	Outcome string
	Cursor  string
	Limit   int
}

// EventSummary é uma linha da lista de eventos.
type EventSummary struct {
	EventID       string
	EventType     string
	Outcome       string
	MerchantID    string
	TransactionID string
	PaymentID     string
	Direction     string
	AmountCents   int64
	Currency      string
	OccurredAt    *time.Time
	ReceivedAt    time.Time
	OutboxStatus  string
	Deliveries    int
}

// LedgerView é o lançamento gerado por um evento.
type LedgerView struct {
	ID                   int64
	TransactionID        string
	PaymentID            string
	MerchantID           string
	Direction            string
	AmountCents          int64
	Currency             string
	Kind                 string
	AdjustsTransactionID string
	OccurredAt           time.Time
	RecordedAt           time.Time
}

// OutboxView é a mensagem de integração de um lançamento.
type OutboxView struct {
	ID            int64
	MessageID     string
	RoutingKey    string
	Status        string
	Attempts      int
	LastError     string
	CreatedAt     time.Time
	PublishedAt   *time.Time
	NextAttemptAt time.Time
	// Archived: a linha do outbox foi apagada pela retenção; só resta o fato de
	// que a mensagem foi publicada (e quando). Os demais campos ficam vazios.
	Archived bool
}

// ConsumptionView registra que um consumidor aplicou a mensagem.
type ConsumptionView struct {
	Consumer   string
	ConsumedAt time.Time
}

// EventTrail é o caminho completo de um evento: webhook, ledger, outbox,
// consumidores e, se houver, passagens pela DLQ.
type EventTrail struct {
	Event       EventSummary
	Payload     json.RawMessage
	Ledger      *LedgerView
	Outbox      *OutboxView
	Consumers   []ConsumptionView
	DeadLetters []DeadLetter
}

// MerchantBalance é o saldo de uma conta de lojista no ledger, comparado ao
// que a projeção do consumidor já refletiu.
type MerchantBalance struct {
	MerchantID          string
	Currency            string
	Payments            int
	CapturedCents       int64
	RefundedCents       int64
	AdjustmentCents     int64
	LedgerNetCents      int64
	ProjectedNetCents   int64
	LastEntryAt         *time.Time
	UnprojectedMessages int
}

// PaymentBalance é o saldo de um pagamento na projeção mantida pelo consumidor.
type PaymentBalance struct {
	PaymentID      string
	MerchantID     string
	Currency       string
	CapturedCents  int64
	RefundedCents  int64
	LedgerNetCents int64
	UpdatedAt      time.Time
}

// PipelineHealth resume o estado do outbox e da DLQ.
type PipelineHealth struct {
	OutboxPending        int
	OutboxDead           int
	OldestPendingAge     time.Duration
	PublishedLastMinute  int
	PublishedLastHour    int
	DeadLettersOpen      int
	OldestDeadLetterAge  time.Duration
	LastConsumedAt       *time.Time
	LastReconciliationAt *time.Time
}

// DeadLetterSource diz de onde vem a mensagem morta.
type DeadLetterSource string

// Origens possíveis: a DLQ do broker ou o outbox que esgotou as tentativas.
const (
	SourceBroker DeadLetterSource = "broker"
	SourceOutbox DeadLetterSource = "outbox"
)

// DeadLetter é uma mensagem que não chegou ao consumidor.
type DeadLetter struct {
	ID             int64
	Source         DeadLetterSource
	MessageID      string
	RoutingKey     string
	Payload        []byte
	Reason         string
	Error          string
	Deliveries     int
	FirstFailedAt  time.Time
	Status         string
	ResolvedBy     string
	ResolvedAt     *time.Time
	ResolutionNote string
	HasOutbox      bool
}

// ArchivedMessage é o que o consumidor da DLQ sabe de uma mensagem morta.
type ArchivedMessage struct {
	MessageID     string
	RoutingKey    string
	Payload       []byte
	Reason        string
	Error         string
	Deliveries    int
	FirstFailedAt time.Time
}

// LedgerFilter filtra a consulta de lançamentos. Cursor é opaco para quem chama.
type LedgerFilter struct {
	MerchantID string
	PaymentID  string
	Cursor     string
	Limit      int
}

// OperationsReader é o lado de leitura do painel.
type OperationsReader interface {
	// LedgerEntries lista lançamentos por id crescente, com paginação por cursor.
	LedgerEntries(ctx context.Context, f LedgerFilter) (Page[LedgerView], error)
	ListEvents(ctx context.Context, f EventFilter) (Page[EventSummary], error)
	EventTrail(ctx context.Context, eventID string) (EventTrail, error)
	MerchantBalances(ctx context.Context) ([]MerchantBalance, error)
	PaymentBalances(ctx context.Context, merchantID, cursor string, limit int) (Page[PaymentBalance], error)
	PipelineHealth(ctx context.Context, now time.Time) (PipelineHealth, error)
	ListDeadLetters(ctx context.Context, status, messageID string) ([]DeadLetter, error)
}

// DeadLetterRepository grava e trata mensagens mortas. Requeue rearma no
// outbox a mensagem original (mesmo message_id), então o consumidor continua
// deduplicando; mensagem sem origem no outbox devolve ErrNoOutboxOrigin.
type DeadLetterRepository interface {
	Archive(ctx context.Context, m ArchivedMessage) error
	Requeue(ctx context.Context, source DeadLetterSource, id int64, operator, note string) error
	Discard(ctx context.Context, source DeadLetterSource, id int64, operator, note string) error
}

// StatementObject é um extrato guardado no bucket.
type StatementObject struct {
	Date         time.Time
	Ref          string
	Size         int64
	SHA256       string
	LastModified time.Time
}

// StatementStore guarda e lista extratos do provedor.
type StatementStore interface {
	Put(ctx context.Context, date time.Time, body []byte) (StatementObject, error)
	List(ctx context.Context) ([]StatementObject, error)
	Stat(ctx context.Context, date time.Time) (StatementObject, error)
}

// RunTrigger é a origem de uma execução da conciliação.
type RunTrigger string

// Origens de execução.
const (
	TriggerManual         RunTrigger = "manual"
	TriggerS3Notification RunTrigger = "s3_notification"
	TriggerCLI            RunTrigger = "cli"
)

// RunRecord descreve uma execução para persistência.
type RunRecord struct {
	Date        time.Time
	Statement   StatementObject
	Trigger     RunTrigger
	TriggeredBy string
	StartedAt   time.Time
	FinishedAt  time.Time
	Err         error
}

// RunSummary é uma execução já gravada.
type RunSummary struct {
	ID             int64
	Date           time.Time
	StatementRef   string
	Trigger        RunTrigger
	TriggeredBy    string
	Status         string
	Error          string
	StatementLines int
	LedgerEntries  int
	Matched        int
	Reversed       int
	StatementNet   int64
	LedgerNet      int64
	Counts         map[string]int
	StartedAt      time.Time
	FinishedAt     time.Time
}

// RunItem é uma linha da partida dobrada.
type RunItem struct {
	Kind          string
	TransactionID string
	Statement     *domain.StatementLine
	Ledger        *domain.LedgerEntry
	Adjusted      bool
	DeltaCents    int64
	Detail        string
	CaseID        *int64
	CaseStatus    string
}

// ReconciliationStore persiste execuções e mantém os casos de divergência:
// cria casos novos, atualiza os que reaparecem e resolve os que sumiram.
type ReconciliationStore interface {
	SaveRun(ctx context.Context, rec RunRecord, report *domain.Report) (RunSummary, error)
	LatestRun(ctx context.Context, date time.Time) (RunSummary, error)
	ListRuns(ctx context.Context, limit int) ([]RunSummary, error)
	RunItems(ctx context.Context, runID int64) ([]RunItem, error)
}

// Case é um caso de divergência.
type Case struct {
	ID            int64
	Date          time.Time
	TransactionID string
	Kind          domain.DivergenceKind
	Status        domain.CaseStatus
	Divergence    domain.Divergence
	DeltaCents    int64
	Detail        string
	Explanation   string
	ResolvedBy    string
	ResolvedAt    *time.Time
	UpdatedAt     time.Time
	Adjustments   []Adjustment
}

// Adjustment é um lançamento de ajuste proposto para um caso.
type Adjustment struct {
	ID           int64
	CaseID       int64
	Entry        domain.LedgerEntry
	Reason       string
	Status       domain.AdjustmentStatus
	ProposedBy   string
	ProposedByID string
	ProposedAt   time.Time
	DecidedBy    string
	DecidedAt    *time.Time
	DecisionNote string
	EventID      string
}

// CaseFilter filtra casos.
type CaseFilter struct {
	Status string
	Date   *time.Time
}

// CaseRepository guarda casos e ajustes. Os métodos de escrita travam a
// linha e chamam decide para aplicar as regras do domínio antes de gravar.
type CaseRepository interface {
	GetCase(ctx context.Context, id int64) (Case, error)
	ListCases(ctx context.Context, f CaseFilter) ([]Case, error)
	PendingAdjustments(ctx context.Context) ([]Adjustment, error)
	Explain(ctx context.Context, id int64, decide func(Case) error, operator, reason string) error
	ProposeAdjustment(ctx context.Context, caseID int64, build func(Case) (domain.LedgerEntry, error),
		actor Actor, reason string) (Adjustment, error)
	ApproveAdjustment(ctx context.Context, id int64, decide func(Adjustment) (Recording, error),
		actor Actor, note string) (Adjustment, error)
	RejectAdjustment(ctx context.Context, id int64, decide func(Adjustment) error,
		actor Actor, note string) (Adjustment, error)
}
