package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeRetention struct {
	outbox, events []int // lotes devolvidos em cada chamada
	cutoffs        []time.Time
}

func (f *fakeRetention) PurgePublishedOutbox(_ context.Context, before time.Time, _ int) (int, error) {
	f.cutoffs = append(f.cutoffs, before)
	n := f.outbox[0]
	f.outbox = f.outbox[1:]
	return n, nil
}

func (f *fakeRetention) ArchiveEventPayloads(_ context.Context, before time.Time, _ int) (int, error) {
	f.cutoffs = append(f.cutoffs, before)
	n := f.events[0]
	f.events = f.events[1:]
	return n, nil
}

type fakeLedgerArchive struct {
	months   []time.Time
	rows     map[string][]LedgerArchiveRow
	askedFor time.Time
	recorded []LedgerArchive
	recErr   error
	limit    int
}

func (f *fakeLedgerArchive) PendingLedgerMonths(_ context.Context, before time.Time) ([]time.Time, error) {
	f.askedFor = before
	return f.months, nil
}

func (f *fakeLedgerArchive) LedgerMonthRows(_ context.Context, m time.Time, limit int) ([]LedgerArchiveRow, error) {
	f.limit = limit
	rows := f.rows[m.Format("2006-01")]
	return rows[:min(limit, len(rows))], nil
}

func (f *fakeLedgerArchive) RecordLedgerArchive(_ context.Context, a LedgerArchive) (bool, error) {
	if f.recErr != nil {
		return false, f.recErr
	}
	f.recorded = append(f.recorded, a)
	return true, nil
}

type fakeArchiveWriter struct{ objects map[string][]byte }

func (w *fakeArchiveWriter) PutArchive(_ context.Context, key string, body []byte) (string, error) {
	w.objects[key] = body
	return "mem://" + key, nil
}

var goodPolicy = RetentionPolicy{
	OutboxPublishedAfter: 7 * 24 * time.Hour, EventPayloadsAfter: 30 * 24 * time.Hour,
	LedgerExportAfter: 90 * 24 * time.Hour, BatchSize: 2,
}

func TestRetentionPolicyRefusesDangerousValues(t *testing.T) {
	for name, p := range map[string]RetentionPolicy{
		"zeros":        {},
		"outbox curto": {OutboxPublishedAfter: time.Minute, EventPayloadsAfter: 48 * time.Hour, LedgerExportAfter: 48 * time.Hour, BatchSize: 1},
		"lote zero":    {OutboxPublishedAfter: 2 * time.Hour, EventPayloadsAfter: 48 * time.Hour, LedgerExportAfter: 48 * time.Hour},
	} {
		if _, err := NewRetentionService(p, nil, nil, nil, discardLogger()); err == nil {
			t.Errorf("%s: a política deve ser recusada", name)
		}
	}
}

func TestRetentionRunDrainsBatchesAndExportsOnlyClosedMonths(t *testing.T) {
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	rep := &fakeRetention{outbox: []int{2, 2, 1}, events: []int{0}}
	ledger := &fakeLedgerArchive{
		months: []time.Time{time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)},
		rows: map[string][]LedgerArchiveRow{"2026-06": {
			{ID: 1, EventID: "e1", TransactionID: "t1", PaymentID: "p", Direction: "credit", AmountCents: 100, Currency: "BRL",
				OccurredAt: now, RecordedAt: now, Kind: "provider"},
		}},
	}
	w := &fakeArchiveWriter{objects: map[string][]byte{}}
	svc, err := NewRetentionService(goodPolicy, rep, ledger, w, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.Run(t.Context(), now)
	if err != nil {
		t.Fatal(err)
	}
	if got.OutboxPurged != 5 || got.EventsArchived != 0 || strings.Join(got.LedgerMonthsArchived, ",") != "2026-06" {
		t.Fatalf("report = %+v", got)
	}
	// 90 dias antes de 22/11 é 24/08: só meses anteriores a agosto estão fechados.
	if want := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC); !ledger.askedFor.Equal(want) {
		t.Fatalf("closedBefore = %s, want %s", ledger.askedFor, want)
	}
	if want := now.Add(-goodPolicy.OutboxPublishedAfter); !rep.cutoffs[0].Equal(want) {
		t.Fatalf("outbox cutoff = %s, want %s", rep.cutoffs[0], want)
	}
	body := string(w.objects["ledger/2026-06.csv"])
	if !strings.HasPrefix(body, "id,event_id,") || !strings.Contains(body, "t1") || len(ledger.recorded) != 1 || ledger.recorded[0].RowCount != 1 {
		t.Fatalf("export = %q, recorded = %+v", body, ledger.recorded)
	}
}

// Se o registro da exportação falhar, o erro aparece (o job falha visível) e o
// mês não conta como arquivado; a próxima execução regrava o mesmo conteúdo.
func TestRetentionRunSurfacesRecordFailureAndExportIsDeterministic(t *testing.T) {
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	rows := []LedgerArchiveRow{{ID: 1, TransactionID: "t1", Currency: "BRL", OccurredAt: now, RecordedAt: now}, {ID: 2, TransactionID: "t2", OccurredAt: now, RecordedAt: now}}
	ledger := &fakeLedgerArchive{
		months: []time.Time{time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)},
		rows:   map[string][]LedgerArchiveRow{"2026-06": rows}, recErr: errors.New("banco fora"),
	}
	w := &fakeArchiveWriter{objects: map[string][]byte{}}
	svc, _ := NewRetentionService(goodPolicy, &fakeRetention{outbox: []int{0}, events: []int{0}}, ledger, w, discardLogger())
	rep, err := svc.Run(t.Context(), now)
	if err == nil || len(rep.LedgerMonthsArchived) != 0 {
		t.Fatalf("Run = %+v, %v, want error and no archived month", rep, err)
	}
	a, _ := LedgerCSV(rows)
	b, _ := LedgerCSV(rows)
	if string(a) != string(b) || string(w.objects["ledger/2026-06.csv"]) != string(a) {
		t.Fatal("a exportação deve ser determinística")
	}
}

// O repositório nunca recebe um limite maior que o máximo mais um, e um mês
// acima do máximo falha sem exportar nada nem registrar.
func TestRetentionRunRefusesOversizedMonthWithoutExporting(t *testing.T) {
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	big := make([]LedgerArchiveRow, MaxLedgerExportRows+1)
	ledger := &fakeLedgerArchive{
		months: []time.Time{time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)},
		rows:   map[string][]LedgerArchiveRow{"2026-06": big},
	}
	w := &fakeArchiveWriter{objects: map[string][]byte{}}
	svc, _ := NewRetentionService(goodPolicy, &fakeRetention{outbox: []int{0}, events: []int{0}}, ledger, w, discardLogger())
	rep, err := svc.Run(t.Context(), now)
	if err == nil || len(rep.LedgerMonthsArchived) != 0 || len(w.objects) != 0 || len(ledger.recorded) != 0 {
		t.Fatalf("Run = %+v, %v, objects=%d, recorded=%d", rep, err, len(w.objects), len(ledger.recorded))
	}
	if ledger.limit != MaxLedgerExportRows+1 {
		t.Fatalf("limit pedido = %d, want %d", ledger.limit, MaxLedgerExportRows+1)
	}
}
