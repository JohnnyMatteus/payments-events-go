// Package app contém os casos de uso e as portas (interfaces) que os
// adaptadores implementam. Depende apenas do domínio.
package app

import (
	"context"
	"errors"
	"time"

	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
)

// ErrPermanent marca falhas que não se resolvem com nova tentativa
// (mensagem mal formada, contrato violado). O consumidor envia essas
// mensagens direto para a DLQ.
var ErrPermanent = errors.New("permanent failure")

// IngestOutcome é o resultado do processamento de um webhook.
type IngestOutcome string

// Resultados possíveis da ingestão. Todos são respondidos com 200 ao provedor.
const (
	// OutcomeRecorded: evento novo, lançamento e mensagem de outbox gravados.
	OutcomeRecorded IngestOutcome = "recorded"
	// OutcomeDuplicate: o mesmo event_id já foi processado; nada foi alterado.
	OutcomeDuplicate IngestOutcome = "duplicate"
	// OutcomeIgnored: tipo de evento sem efeito financeiro; registrado só para auditoria.
	OutcomeIgnored IngestOutcome = "ignored"
	// OutcomeDuplicateTransaction: event_id novo, mas a transação já tinha
	// lançamento (o provedor emitiu dois eventos para a mesma operação).
	OutcomeDuplicateTransaction IngestOutcome = "duplicate_transaction"
)

// Recording é tudo o que precisa ser gravado atomicamente para um evento.
// Entry e Message são nil quando o evento não tem efeito financeiro.
type Recording struct {
	Event      domain.Event
	RawPayload []byte
	Entry      *domain.LedgerEntry
	Message    *OutboxMessage
}

// EventRecorder grava o evento, o lançamento e a mensagem de outbox numa
// única transação, usando o event_id como chave de idempotência.
type EventRecorder interface {
	Record(ctx context.Context, rec Recording) (IngestOutcome, error)
}

// OutboxMessage é uma mensagem aguardando publicação no broker.
type OutboxMessage struct {
	ID         int64
	MessageID  string
	RoutingKey string
	Payload    []byte
	Headers    map[string]string
	Attempts   int
	CreatedAt  time.Time
}

// DeliveryStatus é a decisão do relay para uma mensagem após tentar publicá-la.
type DeliveryStatus int

// Decisões possíveis do relay.
const (
	DeliveryPublished DeliveryStatus = iota + 1
	DeliveryRetry
	DeliveryDead
)

// Delivery descreve como o repositório deve atualizar a mensagem.
type Delivery struct {
	Status        DeliveryStatus
	NextAttemptAt time.Time
	Err           error
}

// OutboxRepository entrega mensagens vencidas ao handler e persiste a
// decisão de cada uma. Implementações devem permitir várias instâncias do
// relay em paralelo sem entregar a mesma mensagem duas vezes ao mesmo tempo.
type OutboxRepository interface {
	ProcessDue(ctx context.Context, now time.Time, limit int,
		handle func(context.Context, OutboxMessage) Delivery) (int, error)
}

// Publisher publica uma mensagem no broker e só retorna nil após a
// confirmação de recebimento (publisher confirm).
type Publisher interface {
	Publish(ctx context.Context, msg OutboxMessage) error
}

// LedgerReader lê lançamentos para a conciliação.
type LedgerReader interface {
	EntriesBetween(ctx context.Context, from, to time.Time) ([]domain.LedgerEntry, error)
}

// StatementReader lê o extrato de liquidação do provedor. ref identifica o
// arquivo (ex.: s3://bucket/chave ou um caminho local); o adaptador decide
// onde buscar e como interpretar o formato.
type StatementReader interface {
	ReadStatement(ctx context.Context, ref string) ([]domain.StatementLine, error)
}

// ProjectionRepository aplica uma mensagem à projeção de saldos no máximo
// uma vez por consumidor. Retorna false quando a mensagem já foi aplicada.
type ProjectionRepository interface {
	ApplyOnce(ctx context.Context, consumer, messageID string, msg LedgerEntryRecorded) (bool, error)
}

// ClaimState é o que o atalho de idempotência sabe sobre um event_id.
type ClaimState int

// Estados possíveis de um claim.
const (
	// ClaimAcquired: ninguém viu o evento; este processo segue para o banco.
	ClaimAcquired ClaimState = iota + 1
	// ClaimInFlight: outro processo está gravando o mesmo evento agora.
	ClaimInFlight
	// ClaimDone: o evento já foi gravado com sucesso.
	ClaimDone
)

// IdempotencyCache é um atalho na frente do banco para reentregas. Não é a
// garantia de idempotência (essa é a constraint única no Postgres, ADR 0002):
// perder o cache só faz a requisição ir ao banco.
type IdempotencyCache interface {
	Claim(ctx context.Context, eventID string) (ClaimState, error)
	Complete(ctx context.Context, eventID string) error
	Release(ctx context.Context, eventID string) error
}

// RateDecision é a resposta do limitador para uma chave.
type RateDecision struct {
	Allowed    bool
	Limit      int
	Remaining  int
	RetryAfter time.Duration
}

// RateLimiter conta requisições por chave numa janela compartilhada entre
// réplicas.
type RateLimiter interface {
	Allow(ctx context.Context, key string) (RateDecision, error)
}

// SecretStore lê um segredo por referência (o formato da referência é do
// adaptador: Vault "caminho#campo", Secrets Manager "id#campo"). Quem chama
// nunca grava o valor em log.
type SecretStore interface {
	Get(ctx context.Context, ref string) (string, error)
}
