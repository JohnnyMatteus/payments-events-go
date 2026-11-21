package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
)

func TestLoggerAddsTraceContext(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, "info", "test-service")

	tel, err := Setup(context.Background(), "test-service", "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tel.Shutdown(context.Background()) })

	ctx, span := otel.Tracer("test").Start(context.Background(), "op")
	logger.With("component", "x").InfoContext(ctx, "hello")
	logger.DebugContext(ctx, "filtered by level")
	span.End()

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected one log line, got %d: %s", len(lines), buf.String())
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["trace_id"] != span.SpanContext().TraceID().String() || entry["service"] != "test-service" || entry["component"] != "x" {
		t.Fatalf("log entry = %v", entry)
	}
}

func TestMetricsEndpointExposesRelayMetrics(t *testing.T) {
	tel, err := Setup(context.Background(), "test-service", "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tel.Shutdown(context.Background()) })

	m, err := NewRelayMetrics()
	if err != nil {
		t.Fatal(err)
	}
	m.Published(150 * time.Millisecond)
	m.Retried(1)
	m.DeadLettered()

	rec := httptest.NewRecorder()
	tel.MetricsHandler.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	for _, want := range []string{"outbox_published_total", "outbox_retries_total", "outbox_dead_total",
		"outbox_publish_lag_seconds_bucket", "go_goroutines"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("/metrics missing %s", want)
		}
	}
}
