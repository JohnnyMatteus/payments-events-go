package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/JohnnyMatteus/payments-events-go/internal/app"
	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
)

type stubReader struct {
	app.OperationsReader
	letters []app.DeadLetter
}

func (s stubReader) ListDeadLetters(context.Context, string, string) ([]app.DeadLetter, error) {
	return s.letters, nil
}

type stubDeadLetters struct{ calls []string }

func (s *stubDeadLetters) Archive(context.Context, app.ArchivedMessage) error { return nil }
func (s *stubDeadLetters) Requeue(_ context.Context, _ app.DeadLetterSource, id int64, op, note string) error {
	s.calls = append(s.calls, op+":"+note)
	return nil
}
func (s *stubDeadLetters) Discard(context.Context, app.DeadLetterSource, int64, string, string) error { return nil }

type stubCases struct {
	app.CaseRepository
	adjustment app.Adjustment
}

func (s stubCases) ApproveAdjustment(_ context.Context, _ int64, decide func(app.Adjustment) (app.Recording, error),
	_, _ string) (app.Adjustment, error) {
	_, err := decide(s.adjustment)
	return s.adjustment, err
}

func newTestMux(t *testing.T, deps AdminDeps) http.Handler {
	t.Helper()
	auth, err := NewOperatorAuth([]string{"ana:token-da-ana", "bruno:token-do-bruno"})
	if err != nil {
		t.Fatal(err)
	}
	deps.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	deps.Now = func() time.Time { return time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC) }
	mux := http.NewServeMux()
	NewAdminHandler(deps).Register(mux, auth)
	return mux
}

func call(h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAdminRequiresOperatorToken(t *testing.T) {
	mux := newTestMux(t, AdminDeps{})
	for name, token := range map[string]string{"sem token": "", "token errado": "outro-token"} {
		if rec := call(mux, http.MethodGet, "/api/v1/me", token, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", name, rec.Code)
		}
	}
	rec := call(mux, http.MethodGet, "/api/v1/me", "token-do-bruno", "")
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"bruno"`)) {
		t.Fatalf("me = %d %s", rec.Code, rec.Body)
	}
}

func TestOperatorAuthRejectsWeakConfig(t *testing.T) {
	for _, entries := range [][]string{nil, {"ana"}, {"ana:curto"}, {"ana:12345678", "ANA:87654321"}} {
		if _, err := NewOperatorAuth(entries); err == nil {
			t.Errorf("NewOperatorAuth(%v) must fail", entries)
		}
	}
}

func TestReprocessRecordsOperatorAndRequiresReason(t *testing.T) {
	dl := &stubDeadLetters{}
	mux := newTestMux(t, AdminDeps{DeadLetters: app.NewDeadLetterService(dl)})
	path := "/api/v1/dead-letters/broker/4/reprocess"
	if rec := call(mux, http.MethodPost, path, "token-da-ana", `{}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("without reason: status = %d, want 422", rec.Code)
	}
	if rec := call(mux, http.MethodPost, path, "token-da-ana", `{"reason":"banco voltou"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("with reason: status = %d, want 204", rec.Code)
	}
	if len(dl.calls) != 1 || dl.calls[0] != "ana:banco voltou" {
		t.Fatalf("calls = %v", dl.calls)
	}
}

func TestApproveByProposerIsForbidden(t *testing.T) {
	cases := stubCases{adjustment: app.Adjustment{ID: 1, Status: domain.AdjustmentProposed, ProposedBy: "ana"}}
	mux := newTestMux(t, AdminDeps{CaseService: app.NewCaseService(cases), Cases: cases})
	rec := call(mux, http.MethodPost, "/api/v1/adjustments/1/approve", "token-da-ana", `{}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body)
	}
	var p problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p.Status != http.StatusForbidden {
		t.Fatalf("problem = %+v, %v", p, err)
	}
}

func TestDeadLetterListCarriesAgeAndReprocessability(t *testing.T) {
	failed := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	reader := stubReader{letters: []app.DeadLetter{
		{ID: 1, Source: app.SourceBroker, MessageID: "m1", Status: "open", FirstFailedAt: failed, HasOutbox: true},
		{ID: 2, Source: app.SourceBroker, MessageID: "m2", Status: "open", FirstFailedAt: failed},
	}}
	mux := newTestMux(t, AdminDeps{Reader: reader})
	rec := call(mux, http.MethodGet, "/api/v1/dead-letters", "token-da-ana", "")
	var out struct{ Items []deadLetterDTO }
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Items) != 2 {
		t.Fatalf("body = %s (%v)", rec.Body, err)
	}
	if out.Items[0].AgeSeconds != 3*3600 || !out.Items[0].CanReprocess || out.Items[1].CanReprocess {
		t.Fatalf("items = %+v", out.Items)
	}
}
