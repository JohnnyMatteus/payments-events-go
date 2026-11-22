package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/JohnnyMatteus/payments-events-go/internal/app"
	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
)

// maxStatementBytes limita o extrato enviado pelo painel.
const maxStatementBytes = 5 << 20

// maxActionBody limita o corpo JSON das ações.
const maxActionBody = 16 << 10

// Runner executa a conciliação de um dia.
type Runner interface {
	Run(ctx context.Context, date time.Time, trigger app.RunTrigger, operator string) (app.RunSummary, error)
}

// AdminDeps reúne as portas que a API de operação usa.
type AdminDeps struct {
	Reader      app.OperationsReader
	DeadLetters *app.DeadLetterService
	Statements  *app.StatementService
	Runner      Runner
	Runs        app.ReconciliationStore
	Cases       app.CaseRepository
	CaseService *app.CaseService
	Now         func() time.Time
	Logger      *slog.Logger
}

// AdminHandler serve a API REST de consulta e operação, sob /api/v1.
type AdminHandler struct {
	AdminDeps
}

// NewAdminHandler cria o handler. As rotas exigem operador autenticado.
func NewAdminHandler(deps AdminDeps) *AdminHandler {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &AdminHandler{AdminDeps: deps}
}

// Register liga as rotas ao mux, todas protegidas por auth.
func (h *AdminHandler) Register(mux *http.ServeMux, auth *OperatorAuth) {
	read := func(pattern string, fn http.HandlerFunc) {
		mux.Handle(pattern, auth.RequireRole(RoleReader, fn))
	}
	act := func(pattern string, fn http.HandlerFunc) {
		mux.Handle(pattern, auth.RequireRole(RoleOperator, fn))
	}
	read("GET /api/v1/me", h.me)
	read("GET /api/v1/events", h.listEvents)
	read("GET /api/v1/events/{id}", h.getEvent)
	read("GET /api/v1/balances/merchants", h.merchantBalances)
	read("GET /api/v1/balances/payments", h.paymentBalances)
	read("GET /api/v1/pipeline", h.pipeline)
	read("GET /api/v1/dead-letters", h.listDeadLetters)
	act("POST /api/v1/dead-letters/{source}/{id}/reprocess", h.reprocess)
	act("POST /api/v1/dead-letters/{source}/{id}/discard", h.discard)
	read("GET /api/v1/statements", h.listStatements)
	act("PUT /api/v1/statements/{date}", h.uploadStatement)
	read("GET /api/v1/reconciliation/runs", h.listRuns)
	act("POST /api/v1/reconciliation/runs", h.startRun)
	read("GET /api/v1/reconciliation/runs/latest", h.latestRun)
	read("GET /api/v1/reconciliation/runs/{id}/items", h.runItems)
	read("GET /api/v1/cases", h.listCases)
	read("GET /api/v1/cases/{id}", h.getCase)
	act("POST /api/v1/cases/{id}/explain", h.explainCase)
	act("POST /api/v1/cases/{id}/adjustments", h.proposeAdjustment)
	read("GET /api/v1/adjustments", h.pendingAdjustments)
	act("POST /api/v1/adjustments/{id}/approve", h.approveAdjustment)
	act("POST /api/v1/adjustments/{id}/reject", h.rejectAdjustment)
}

func (h *AdminHandler) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"operator": OperatorFrom(r.Context()), "role": string(RoleFrom(r.Context()))})
}

func (h *AdminHandler) listEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, err := intParam(q.Get("limit"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	page, err := h.Reader.ListEvents(r.Context(), app.EventFilter{
		Search: q.Get("q"), Type: q.Get("type"), Outcome: q.Get("outcome"), Cursor: q.Get("cursor"), Limit: limit,
	})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, pageDTO(page, eventDTOOf))
}

func (h *AdminHandler) getEvent(w http.ResponseWriter, r *http.Request) {
	trail, err := h.Reader.EventTrail(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, trailDTOOf(trail, h.Now()))
}

func (h *AdminHandler) merchantBalances(w http.ResponseWriter, r *http.Request) {
	items, err := h.Reader.MerchantBalances(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": mapSlice(items, merchantBalanceDTOOf)})
}

func (h *AdminHandler) paymentBalances(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, err := intParam(q.Get("limit"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	page, err := h.Reader.PaymentBalances(r.Context(), q.Get("merchant_id"), q.Get("cursor"), limit)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, pageDTO(page, paymentBalanceDTOOf))
}

func (h *AdminHandler) pipeline(w http.ResponseWriter, r *http.Request) {
	health, err := h.Reader.PipelineHealth(r.Context(), h.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, pipelineDTOOf(health))
}

func (h *AdminHandler) listDeadLetters(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "open"
	}
	if status == "all" {
		status = ""
	}
	items, err := h.Reader.ListDeadLetters(r.Context(), status, "")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	now := h.Now()
	writeJSON(w, http.StatusOK, map[string]any{"items": mapSlice(items, func(d app.DeadLetter) deadLetterDTO {
		return deadLetterDTOOf(d, now)
	})})
}

type reasonBody struct {
	Reason string `json:"reason"`
}

func (h *AdminHandler) reprocess(w http.ResponseWriter, r *http.Request) {
	h.deadLetterAction(w, r, h.DeadLetters.Reprocess)
}

func (h *AdminHandler) discard(w http.ResponseWriter, r *http.Request) {
	h.deadLetterAction(w, r, h.DeadLetters.Discard)
}

func (h *AdminHandler) deadLetterAction(w http.ResponseWriter, r *http.Request,
	act func(context.Context, app.DeadLetterSource, int64, string, string) error) {
	id, err := idParam(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var body reasonBody
	if err := decodeBody(r, &body); err != nil {
		h.fail(w, r, err)
		return
	}
	if err := act(r.Context(), app.DeadLetterSource(r.PathValue("source")), id, OperatorFrom(r.Context()), body.Reason); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminHandler) listStatements(w http.ResponseWriter, r *http.Request) {
	items, err := h.Statements.List(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": mapSlice(items, statementDTOOf)})
}

func (h *AdminHandler) uploadStatement(w http.ResponseWriter, r *http.Request) {
	date, err := dateParam(r.PathValue("date"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxStatementBytes))
	if err != nil {
		h.fail(w, r, app.ErrInvalidInput)
		return
	}
	obj, lines, err := h.Statements.Upload(r.Context(), date, body)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"statement": statementDTOOf(obj), "lines": lines})
}

func (h *AdminHandler) startRun(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Date string `json:"date"`
	}
	if err := decodeBody(r, &body); err != nil {
		h.fail(w, r, err)
		return
	}
	date, err := dateParam(body.Date)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	summary, err := h.Runner.Run(r.Context(), date, app.TriggerManual, OperatorFrom(r.Context()))
	if err != nil && summary.ID == 0 {
		h.fail(w, r, err)
		return
	}
	// Falha de leitura do extrato é um resultado gravado, não erro da requisição.
	writeJSON(w, http.StatusCreated, runDTOOf(summary))
}

func (h *AdminHandler) listRuns(w http.ResponseWriter, r *http.Request) {
	limit, err := intParam(r.URL.Query().Get("limit"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	runs, err := h.Runs.ListRuns(r.Context(), limit)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": mapSlice(runs, runDTOOf)})
}

func (h *AdminHandler) latestRun(w http.ResponseWriter, r *http.Request) {
	date, err := dateParam(r.URL.Query().Get("date"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	run, err := h.Runs.LatestRun(r.Context(), date)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, runDTOOf(run))
}

func (h *AdminHandler) runItems(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items, err := h.Runs.RunItems(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": mapSlice(items, runItemDTOOf)})
}

func (h *AdminHandler) listCases(w http.ResponseWriter, r *http.Request) {
	filter := app.CaseFilter{Status: r.URL.Query().Get("status")}
	if d := r.URL.Query().Get("date"); d != "" {
		date, err := dateParam(d)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		filter.Date = &date
	}
	cases, err := h.Cases.ListCases(r.Context(), filter)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": mapSlice(cases, caseDTOOf)})
}

func (h *AdminHandler) getCase(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	c, err := h.Cases.GetCase(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, caseDTOOf(c))
}

func (h *AdminHandler) explainCase(w http.ResponseWriter, r *http.Request) {
	id, body, ok := h.idAndReason(w, r)
	if !ok {
		return
	}
	if err := h.CaseService.Explain(r.Context(), id, OperatorFrom(r.Context()), body.Reason); err != nil {
		h.fail(w, r, err)
		return
	}
	h.respondCase(w, r, id, http.StatusOK)
}

func (h *AdminHandler) proposeAdjustment(w http.ResponseWriter, r *http.Request) {
	id, body, ok := h.idAndReason(w, r)
	if !ok {
		return
	}
	if _, err := h.CaseService.ProposeAdjustment(r.Context(), id, OperatorFrom(r.Context()), body.Reason); err != nil {
		h.fail(w, r, err)
		return
	}
	h.respondCase(w, r, id, http.StatusCreated)
}

func (h *AdminHandler) pendingAdjustments(w http.ResponseWriter, r *http.Request) {
	items, err := h.Cases.PendingAdjustments(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": mapSlice(items, adjustmentDTOOf)})
}

func (h *AdminHandler) approveAdjustment(w http.ResponseWriter, r *http.Request) {
	id, body, ok := h.idAndReason(w, r)
	if !ok {
		return
	}
	a, err := h.CaseService.ApproveAdjustment(r.Context(), id, OperatorFrom(r.Context()), body.Reason)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, adjustmentDTOOf(a))
}

func (h *AdminHandler) rejectAdjustment(w http.ResponseWriter, r *http.Request) {
	id, body, ok := h.idAndReason(w, r)
	if !ok {
		return
	}
	a, err := h.CaseService.RejectAdjustment(r.Context(), id, OperatorFrom(r.Context()), body.Reason)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, adjustmentDTOOf(a))
}

func (h *AdminHandler) respondCase(w http.ResponseWriter, r *http.Request, id int64, status int) {
	c, err := h.Cases.GetCase(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, status, caseDTOOf(c))
}

func (h *AdminHandler) idAndReason(w http.ResponseWriter, r *http.Request) (int64, reasonBody, bool) {
	id, err := idParam(r)
	var body reasonBody
	if err == nil {
		err = decodeBody(r, &body)
	}
	if err != nil {
		h.fail(w, r, err)
		return 0, body, false
	}
	return id, body, true
}

// fail traduz o erro da aplicação para o status HTTP. Detalhes internos
// ficam no log; a resposta só traz o que o operador precisa para agir.
func (h *AdminHandler) fail(w http.ResponseWriter, r *http.Request, err error) {
	status, code, detail := classify(err)
	if status == http.StatusInternalServerError {
		h.Logger.ErrorContext(r.Context(), "admin request failed", slog.String("path", r.URL.Path), slog.Any("error", err))
	}
	writeProblem(w, status, code, detail)
}

func classify(err error) (int, string, string) {
	switch {
	case errors.Is(err, app.ErrNotFound):
		return http.StatusNotFound, "not_found", "registro não encontrado"
	case errors.Is(err, app.ErrStatementMissing):
		return http.StatusNotFound, "statement_missing", "o extrato desse dia ainda não chegou ao bucket"
	case errors.Is(err, domain.ErrReasonRequired):
		return http.StatusUnprocessableEntity, "reason_required", "informe o motivo da decisão"
	case errors.Is(err, domain.ErrSameOperator):
		return http.StatusForbidden, "same_operator", "quem propôs o ajuste não pode aprová-lo"
	case errors.Is(err, domain.ErrNoAdjustment):
		return http.StatusUnprocessableEntity, "no_adjustment", "esta divergência não pode ser corrigida por ajuste: " + err.Error()
	case errors.Is(err, app.ErrNoOutboxOrigin):
		return http.StatusConflict, "no_outbox_origin", "a mensagem não tem origem no outbox para ser reenviada"
	case errors.Is(err, domain.ErrCaseNotOpen), errors.Is(err, domain.ErrAdjustmentClosed), errors.Is(err, app.ErrConflict):
		return http.StatusConflict, "conflict", err.Error()
	case errors.Is(err, app.ErrInvalidInput):
		return http.StatusBadRequest, "invalid_input", err.Error()
	default:
		return http.StatusInternalServerError, "internal", "erro interno"
	}
}

func decodeBody(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxActionBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return app.ErrInvalidInput
	}
	return nil
}

func idParam(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, app.ErrInvalidInput
	}
	return id, nil
}

func intParam(v string) (int, error) {
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, app.ErrInvalidInput
	}
	return n, nil
}

func dateParam(v string) (time.Time, error) {
	d, err := time.Parse(time.DateOnly, v)
	if err != nil {
		return time.Time{}, errors.Join(app.ErrInvalidInput, errors.New("data deve ser AAAA-MM-DD"))
	}
	return d, nil
}
