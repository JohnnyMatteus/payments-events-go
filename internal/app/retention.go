package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"
)

// Limites mínimos da política: um valor zero ou minúsculo (variável vazia,
// unidade errada) apagaria ou arquivaria dados que ainda estão em uso.
const (
	minOutboxRetention  = time.Hour
	minPayloadRetention = 24 * time.Hour
	minLedgerExport     = 24 * time.Hour
	// MaxLedgerExportRows protege a memória do job: um mês maior que isso
	// falha em vez de ser exportado pela metade.
	MaxLedgerExportRows = 2_000_000
)

// RetentionPolicy diz quanto cada dado fica no lugar quente. Os prazos não
// vêm de exigência legal (o autor não definiu nenhuma): são configuráveis.
type RetentionPolicy struct {
	// OutboxPublishedAfter: mensagens publicadas do outbox são apagadas depois disso.
	OutboxPublishedAfter time.Duration
	// EventPayloadsAfter: o payload completo de processed_events vai para o
	// arquivo particionado; a linha (e a idempotência) fica.
	EventPayloadsAfter time.Duration
	// LedgerExportAfter: meses do ledger mais antigos que isso são exportados
	// para o bucket de arquivo. O ledger em si nunca é alterado nem apagado.
	LedgerExportAfter time.Duration
	BatchSize         int
}

// Validate recusa políticas que apagariam dado em uso.
func (p RetentionPolicy) Validate() error {
	var errs []error
	if p.OutboxPublishedAfter < minOutboxRetention {
		errs = append(errs, fmt.Errorf("outbox retention must be at least %s", minOutboxRetention))
	}
	if p.EventPayloadsAfter < minPayloadRetention {
		errs = append(errs, fmt.Errorf("event payload retention must be at least %s", minPayloadRetention))
	}
	if p.LedgerExportAfter < minLedgerExport {
		errs = append(errs, fmt.Errorf("ledger export delay must be at least %s", minLedgerExport))
	}
	if p.BatchSize < 1 {
		errs = append(errs, errors.New("retention batch size must be positive"))
	}
	return errors.Join(errs...)
}

// RetentionRepository cuida do que pode sair do lugar quente sem perder garantia.
type RetentionRepository interface {
	// PurgePublishedOutbox apaga até limit mensagens publicadas antes de before.
	// Pendentes, mortas e descartadas ficam: alguém ainda precisa delas.
	PurgePublishedOutbox(ctx context.Context, before time.Time, limit int) (int, error)
	// ArchiveEventPayloads copia o payload completo de até limit eventos
	// recebidos antes de before para o arquivo e reduz o payload da linha a um
	// resumo. A linha de processed_events nunca é apagada.
	ArchiveEventPayloads(ctx context.Context, before time.Time, limit int) (int, error)
}

// LedgerArchiveRow é um lançamento como vai para o arquivo.
type LedgerArchiveRow struct {
	ID                   int64
	EventID              string
	MerchantID           string
	TransactionID        string
	PaymentID            string
	Direction            string
	AmountCents          int64
	Currency             string
	OccurredAt           time.Time
	RecordedAt           time.Time
	Kind                 string
	AdjustsTransactionID string
}

// LedgerArchive registra a exportação de um mês do ledger.
type LedgerArchive struct {
	Month     time.Time
	ObjectRef string
	RowCount  int64
	SHA256    string
}

// LedgerArchiveRepository lê o ledger por mês e guarda o registro da exportação.
type LedgerArchiveRepository interface {
	// PendingLedgerMonths lista os meses (primeiro dia, UTC) com lançamentos
	// gravados antes de before e ainda sem exportação registrada.
	PendingLedgerMonths(ctx context.Context, before time.Time) ([]time.Time, error)
	// LedgerMonthRows devolve os lançamentos do mês, por id.
	LedgerMonthRows(ctx context.Context, month time.Time) ([]LedgerArchiveRow, error)
	// RecordLedgerArchive grava a exportação; false se o mês já estava registrado.
	RecordLedgerArchive(ctx context.Context, a LedgerArchive) (bool, error)
}

// ArchiveWriter grava um objeto no bucket de arquivo e devolve sua referência.
type ArchiveWriter interface {
	PutArchive(ctx context.Context, key string, body []byte) (string, error)
}

// RetentionReport resume uma execução.
type RetentionReport struct {
	OutboxPurged         int
	EventsArchived       int
	LedgerMonthsArchived []string
}

// RetentionService aplica a política. Roda como job (CronJob), nunca no
// caminho do webhook.
type RetentionService struct {
	policy  RetentionPolicy
	repo    RetentionRepository
	ledger  LedgerArchiveRepository
	archive ArchiveWriter
	logger  *slog.Logger
}

// NewRetentionService valida a política e monta o serviço.
func NewRetentionService(policy RetentionPolicy, repo RetentionRepository, ledger LedgerArchiveRepository,
	archive ArchiveWriter, logger *slog.Logger) (*RetentionService, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return &RetentionService{policy: policy, repo: repo, ledger: ledger, archive: archive, logger: logger}, nil
}

// Run executa as três rotinas. Uma falha numa delas não impede as outras, mas
// o erro volta no fim para o job falhar de forma visível.
func (s *RetentionService) Run(ctx context.Context, now time.Time) (RetentionReport, error) {
	var (
		rep  RetentionReport
		errs []error
		err  error
	)
	if rep.OutboxPurged, err = drain(ctx, s.policy.BatchSize, func() (int, error) {
		return s.repo.PurgePublishedOutbox(ctx, now.Add(-s.policy.OutboxPublishedAfter), s.policy.BatchSize)
	}); err != nil {
		errs = append(errs, fmt.Errorf("outbox cleanup: %w", err))
	}
	if rep.EventsArchived, err = drain(ctx, s.policy.BatchSize, func() (int, error) {
		return s.repo.ArchiveEventPayloads(ctx, now.Add(-s.policy.EventPayloadsAfter), s.policy.BatchSize)
	}); err != nil {
		errs = append(errs, fmt.Errorf("event payload archive: %w", err))
	}
	if rep.LedgerMonthsArchived, err = s.exportLedger(ctx, now); err != nil {
		errs = append(errs, fmt.Errorf("ledger export: %w", err))
	}
	s.logger.InfoContext(ctx, "retention finished",
		slog.Int("outbox_purged", rep.OutboxPurged), slog.Int("events_archived", rep.EventsArchived),
		slog.Any("ledger_months_archived", rep.LedgerMonthsArchived))
	return rep, errors.Join(errs...)
}

// drain repete o lote até vir menos que o tamanho do lote.
func drain(ctx context.Context, batch int, step func() (int, error)) (int, error) {
	total := 0
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, err := step()
		total += n
		if err != nil {
			return total, err
		}
		if n < batch {
			return total, nil
		}
	}
}

func (s *RetentionService) exportLedger(ctx context.Context, now time.Time) ([]string, error) {
	cutoff := now.Add(-s.policy.LedgerExportAfter).UTC()
	// Só meses inteiros anteriores ao mês do corte: um mês em andamento não está fechado.
	closedBefore := time.Date(cutoff.Year(), cutoff.Month(), 1, 0, 0, 0, 0, time.UTC)
	months, err := s.ledger.PendingLedgerMonths(ctx, closedBefore)
	if err != nil {
		return nil, err
	}
	var done []string
	for _, month := range months {
		if err := s.exportMonth(ctx, month); err != nil {
			return done, fmt.Errorf("month %s: %w", month.Format("2006-01"), err)
		}
		done = append(done, month.Format("2006-01"))
	}
	return done, nil
}

func (s *RetentionService) exportMonth(ctx context.Context, month time.Time) error {
	rows, err := s.ledger.LedgerMonthRows(ctx, month)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	if len(rows) > MaxLedgerExportRows {
		return fmt.Errorf("%d rows exceed the export limit of %d", len(rows), MaxLedgerExportRows)
	}
	body, err := LedgerCSV(rows)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	ref, err := s.archive.PutArchive(ctx, "ledger/"+month.Format("2006-01")+".csv", body)
	if err != nil {
		return err
	}
	// O objeto vem antes do registro: se o registro falhar, a próxima execução
	// regrava o mesmo conteúdo (o bucket é versionado) e tenta de novo.
	if _, err := s.ledger.RecordLedgerArchive(ctx, LedgerArchive{
		Month: month, ObjectRef: ref, RowCount: int64(len(rows)), SHA256: hash,
	}); err != nil {
		return err
	}
	return nil
}

// LedgerCSV serializa os lançamentos de forma determinística (por id), então
// exportar duas vezes o mesmo mês dá o mesmo hash.
func LedgerCSV(rows []LedgerArchiveRow) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write([]string{"id", "event_id", "merchant_id", "transaction_id", "payment_id", "direction",
		"amount_cents", "currency", "occurred_at", "recorded_at", "kind", "adjusts_transaction_id"}); err != nil {
		return nil, err
	}
	for _, r := range rows {
		if err := w.Write([]string{
			strconv.FormatInt(r.ID, 10), r.EventID, r.MerchantID, r.TransactionID, r.PaymentID, r.Direction,
			strconv.FormatInt(r.AmountCents, 10), r.Currency,
			r.OccurredAt.UTC().Format(time.RFC3339Nano), r.RecordedAt.UTC().Format(time.RFC3339Nano),
			r.Kind, r.AdjustsTransactionID,
		}); err != nil {
			return nil, err
		}
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}
