package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// WebhookPath é a rota em que o provedor entrega os eventos.
const WebhookPath = "/webhooks/pagafacil"

// ReadinessCheck verifica uma dependência (ex.: ping no banco).
type ReadinessCheck func(ctx context.Context) error

// NewRouter monta as rotas do serviço de API. O webhook passa pelo middleware
// do OpenTelemetry, que cria o span do servidor (continuando um traceparent
// recebido) e a métrica de duração HTTP. Health e métricas ficam fora do
// tracing para não poluir os traces.
func NewRouter(webhook http.Handler, metrics http.Handler, ready ReadinessCheck, opts ...RouterOption) http.Handler {
	mux := http.NewServeMux()
	for _, opt := range opts {
		opt(mux)
	}
	route := "POST " + WebhookPath
	mux.Handle(route, otelhttp.NewHandler(webhook, route))
	registerOps(mux, metrics, ready)
	return mux
}

// RouterOption acrescenta rotas ao serviço de API.
type RouterOption func(*http.ServeMux)

// WithAdmin liga a API de operação (/api/v1), protegida por token de operador.
func WithAdmin(admin *AdminHandler, auth *OperatorAuth) RouterOption {
	return func(mux *http.ServeMux) { admin.Register(mux, auth) }
}

// NewOpsRouter expõe só health e métricas, para os processos sem API
// (relay e consumidor).
func NewOpsRouter(metrics http.Handler, ready ReadinessCheck) http.Handler {
	mux := http.NewServeMux()
	registerOps(mux, metrics, ready)
	return mux
}

func registerOps(mux *http.ServeMux, metrics http.Handler, ready ReadinessCheck) {
	mux.Handle("GET /metrics", metrics)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", readiness(ready))
}

func readiness(ready ReadinessCheck) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := ready(ctx); err != nil {
			writeProblem(w, http.StatusServiceUnavailable, "not_ready", "dependência indisponível")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}
}

// problem segue o formato RFC 9457 (application/problem+json).
type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
}

func writeProblem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problem{
		Type:   "https://payments-events.example.com/problems/" + code,
		Title:  http.StatusText(status),
		Status: status,
		Detail: detail,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
