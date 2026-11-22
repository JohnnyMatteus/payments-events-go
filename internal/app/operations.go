package app

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
)

// EventTypeLedgerAdjustment é o tipo do evento sintético gravado quando um
// ajuste é aprovado; ele percorre o mesmo caminho ledger → outbox → consumidor.
const EventTypeLedgerAdjustment = "ledger.adjustment"

// StatementParser valida o formato do extrato recebido.
type StatementParser func(io.Reader) ([]domain.StatementLine, error)

// StatementService recebe os extratos do provedor.
type StatementService struct {
	store StatementStore
	parse StatementParser
}

// NewStatementService cria o serviço.
func NewStatementService(store StatementStore, parse StatementParser) *StatementService {
	return &StatementService{store: store, parse: parse}
}

// Upload valida o arquivo e o guarda como o extrato do dia. Um extrato que
// não passa na leitura nunca chega ao bucket: conciliar um arquivo parcial
// esconderia divergências.
func (s *StatementService) Upload(ctx context.Context, date time.Time, body []byte) (StatementObject, int, error) {
	lines, err := s.parse(bytes.NewReader(body))
	if err != nil {
		return StatementObject{}, 0, fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	obj, err := s.store.Put(ctx, date, body)
	return obj, len(lines), err
}

// List devolve os extratos disponíveis.
func (s *StatementService) List(ctx context.Context) ([]StatementObject, error) {
	return s.store.List(ctx)
}

// DeadLetterService trata as mensagens mortas a pedido de um operador.
type DeadLetterService struct {
	repo DeadLetterRepository
}

// NewDeadLetterService cria o serviço.
func NewDeadLetterService(repo DeadLetterRepository) *DeadLetterService {
	return &DeadLetterService{repo: repo}
}

// Reprocess devolve a mensagem ao fluxo pelo outbox. O motivo é obrigatório
// porque reprocessar mexe com saldo.
func (s *DeadLetterService) Reprocess(ctx context.Context, source DeadLetterSource, id int64, operator, note string) error {
	note, err := domain.RequireReason(note)
	if err != nil {
		return err
	}
	return s.repo.Requeue(ctx, source, id, operator, note)
}

// Discard encerra a mensagem sem reprocessar, com motivo.
func (s *DeadLetterService) Discard(ctx context.Context, source DeadLetterSource, id int64, operator, note string) error {
	note, err := domain.RequireReason(note)
	if err != nil {
		return err
	}
	return s.repo.Discard(ctx, source, id, operator, note)
}

// DeadLetterArchiver é o consumidor da DLQ do broker: registra cada mensagem
// morta com o motivo legível para a operação.
type DeadLetterArchiver struct {
	repo DeadLetterRepository
}

// NewDeadLetterArchiver cria o arquivador.
func NewDeadLetterArchiver(repo DeadLetterRepository) *DeadLetterArchiver {
	return &DeadLetterArchiver{repo: repo}
}

// Archive grava a mensagem. O broker não carrega o erro do consumidor, então
// o contrato é validado de novo aqui: para mensagem venenosa, o motivo é o
// mesmo que a mandou para a DLQ; para limite de entregas, o erro foi
// transitório e o contrato é válido.
func (a *DeadLetterArchiver) Archive(ctx context.Context, m ArchivedMessage) error {
	if m.Error == "" {
		m.Error = DiagnoseLedgerMessage(m.MessageID, m.Payload)
	}
	if m.Deliveries < 1 {
		m.Deliveries = 1
	}
	return a.repo.Archive(ctx, m)
}

// DiagnoseLedgerMessage explica por que uma mensagem LedgerEntryRecorded não
// seria aceita pelo consumidor.
func DiagnoseLedgerMessage(messageID string, body []byte) string {
	if err := decodeLedgerMessage(messageID, body); err != nil {
		return strings.TrimPrefix(err.Error(), ErrPermanent.Error()+": ")
	}
	return "contrato válido: falha transitória até o limite de entregas"
}

// ReconciliationRunner executa a conciliação de um dia e grava o resultado,
// venha o pedido do painel, da notificação do S3 ou da linha de comando.
type ReconciliationRunner struct {
	statements StatementStore
	reconcile  *ReconcileService
	store      ReconciliationStore
	logger     *slog.Logger
	now        func() time.Time
}

// NewReconciliationRunner cria o executor.
func NewReconciliationRunner(statements StatementStore, reconcile *ReconcileService,
	store ReconciliationStore, logger *slog.Logger) *ReconciliationRunner {
	return &ReconciliationRunner{statements: statements, reconcile: reconcile, store: store, logger: logger, now: time.Now}
}

// Run concilia o extrato do dia date (UTC) com o ledger. Uma falha de leitura
// do extrato também é gravada, para a tela mostrar o status do job.
func (r *ReconciliationRunner) Run(ctx context.Context, date time.Time, trigger RunTrigger, operator string) (RunSummary, error) {
	date = time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
	obj, err := r.statements.Stat(ctx, date)
	if err != nil {
		return RunSummary{}, err
	}
	rec := RunRecord{Date: date, Statement: obj, Trigger: trigger, TriggeredBy: operator, StartedAt: r.now()}
	period := domain.Period{From: date, To: date.AddDate(0, 0, 1)}
	report, runErr := r.reconcile.Reconcile(ctx, period, obj.Ref)
	rec.FinishedAt = r.now()
	if runErr != nil {
		rec.Err = runErr
		summary, err := r.store.SaveRun(ctx, rec, nil)
		if err != nil {
			return RunSummary{}, errors.Join(runErr, err)
		}
		return summary, runErr
	}
	summary, err := r.store.SaveRun(ctx, rec, &report)
	if err != nil {
		return RunSummary{}, fmt.Errorf("save reconciliation run: %w", err)
	}
	r.logger.InfoContext(ctx, "reconciliation run saved",
		slog.Int64("run_id", summary.ID), slog.String("date", date.Format(time.DateOnly)),
		slog.String("trigger", string(trigger)), slog.Int("divergences", len(report.Divergences)))
	return summary, nil
}

// SettlementPrefix é o prefixo das chaves de extrato no bucket.
const SettlementPrefix = "settlements/"

// SettlementKey é a chave do extrato de um dia.
func SettlementKey(date time.Time) string {
	return SettlementPrefix + date.UTC().Format(time.DateOnly) + ".csv"
}

// SettlementDateFromKey extrai o dia de uma chave settlements/AAAA-MM-DD.csv.
func SettlementDateFromKey(key string) (time.Time, bool) {
	name, ok := strings.CutPrefix(key, SettlementPrefix)
	if !ok {
		return time.Time{}, false
	}
	name, ok = strings.CutSuffix(name, ".csv")
	if !ok {
		return time.Time{}, false
	}
	date, err := time.Parse(time.DateOnly, name)
	return date, err == nil
}

// CaseService aplica as decisões humanas sobre os casos de divergência.
type CaseService struct {
	repo CaseRepository
}

// NewCaseService cria o serviço.
func NewCaseService(repo CaseRepository) *CaseService {
	return &CaseService{repo: repo}
}

// Explain encerra o caso com uma justificativa, sem alterar o ledger.
func (s *CaseService) Explain(ctx context.Context, id int64, operator, reason string) error {
	reason, err := domain.RequireReason(reason)
	if err != nil {
		return err
	}
	return s.repo.Explain(ctx, id, func(c Case) error { return domain.CanExplain(c.Status) }, operator, reason)
}

// ProposeAdjustment registra o lançamento que fecharia a divergência. Ele só
// entra no ledger quando outra pessoa aprovar.
func (s *CaseService) ProposeAdjustment(ctx context.Context, caseID int64, operator, reason string) (Adjustment, error) {
	reason, err := domain.RequireReason(reason)
	if err != nil {
		return Adjustment{}, err
	}
	return s.repo.ProposeAdjustment(ctx, caseID, func(c Case) (domain.LedgerEntry, error) {
		if err := domain.CanExplain(c.Status); err != nil {
			return domain.LedgerEntry{}, err
		}
		return domain.AdjustmentFor(c.Divergence, "c"+strconv.FormatInt(c.ID, 10))
	}, operator, reason)
}

// ApproveAdjustment aplica a regra dos quatro olhos e grava o ajuste no
// ledger e no outbox, na mesma transação que encerra o caso.
func (s *CaseService) ApproveAdjustment(ctx context.Context, id int64, operator, note string) (Adjustment, error) {
	return s.repo.ApproveAdjustment(ctx, id, func(a Adjustment) (Recording, error) {
		if err := domain.CanApprove(a.Status, a.ProposedBy, operator); err != nil {
			return Recording{}, err
		}
		return adjustmentRecording(a, operator)
	}, operator, strings.TrimSpace(note))
}

// RejectAdjustment recusa o ajuste e devolve o caso para aberto.
func (s *CaseService) RejectAdjustment(ctx context.Context, id int64, operator, note string) (Adjustment, error) {
	note, err := domain.RequireReason(note)
	if err != nil {
		return Adjustment{}, err
	}
	return s.repo.RejectAdjustment(ctx, id, func(a Adjustment) error {
		if a.Status != domain.AdjustmentProposed {
			return fmt.Errorf("%w: status %s", domain.ErrAdjustmentClosed, a.Status)
		}
		return nil
	}, operator, note)
}

func adjustmentRecording(a Adjustment, approver string) (Recording, error) {
	entry := a.Entry
	entry.EventID = "adj_" + strconv.FormatInt(a.ID, 10)
	msg, err := newLedgerEntryRecordedMessage(entry)
	if err != nil {
		return Recording{}, err
	}
	// O payload do evento sintético é a trilha de auditoria da decisão.
	raw, err := json.Marshal(map[string]any{
		"adjustment_id": a.ID, "case_id": a.CaseID, "reason": a.Reason,
		"proposed_by": a.ProposedBy, "approved_by": approver,
		"adjusts_transaction_id": entry.AdjustsTransactionID,
	})
	if err != nil {
		return Recording{}, err
	}
	return Recording{
		Event: domain.Event{
			ID: entry.EventID, Type: EventTypeLedgerAdjustment, MerchantID: entry.MerchantID,
			TransactionID: entry.TransactionID, PaymentID: entry.PaymentID, AmountCents: entry.AmountCents,
			Currency: entry.Currency, OccurredAt: entry.OccurredAt,
		},
		RawPayload: raw,
		Entry:      &entry,
		Message:    &msg,
	}, nil
}

// KindMatched marca, em RunItem.Kind, os pares conciliados.
const KindMatched = "matched"

// ItemsOf monta as linhas da partida dobrada na ordem do extrato; os
// lançamentos que só existem no ledger vêm depois, por transação.
func ItemsOf(report domain.Report) []RunItem {
	items := make([]RunItem, 0, len(report.Matches)+len(report.Divergences))
	for i := range report.Matches {
		m := report.Matches[i]
		items = append(items, RunItem{
			Kind: KindMatched, TransactionID: m.Statement.TransactionID,
			Statement: &m.Statement, Ledger: &m.Ledger, Adjusted: m.Adjusted,
		})
	}
	for i := range report.Divergences {
		d := report.Divergences[i]
		items = append(items, RunItem{
			Kind: string(d.Kind), TransactionID: d.TransactionID, Statement: d.Statement, Ledger: d.Ledger,
			DeltaCents: d.DeltaCents(), Detail: d.Detail,
		})
	}
	slices.SortStableFunc(items, func(a, b RunItem) int {
		return cmp.Or(
			cmp.Compare(lineOf(a), lineOf(b)),
			cmp.Compare(a.TransactionID, b.TransactionID),
			cmp.Compare(a.Kind, b.Kind),
		)
	})
	return items
}

// lineOf põe as linhas sem extrato depois das demais.
func lineOf(i RunItem) int {
	if i.Statement == nil {
		return math.MaxInt
	}
	return i.Statement.LineNumber
}
