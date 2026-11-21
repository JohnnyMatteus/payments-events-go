package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func runCmd(t *testing.T, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), args, func(k string) string { return env[k] }, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestRunUsageAndVersion(t *testing.T) {
	t.Parallel()
	if code, _, stderr := runCmd(t, nil); code != exitUsage || !strings.Contains(stderr, "uso: payments") {
		t.Fatalf("no args: code %d stderr %q", code, stderr)
	}
	if code, stdout, _ := runCmd(t, nil, "version"); code != exitOK || strings.TrimSpace(stdout) != "dev" {
		t.Fatalf("version: code %d stdout %q", code, stdout)
	}
	if code, _, stderr := runCmd(t, nil, "deploy"); code != exitUsage || !strings.Contains(stderr, `comando desconhecido "deploy"`) {
		t.Fatalf("unknown: code %d stderr %q", code, stderr)
	}
}

func TestRunRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()
	code, _, stderr := runCmd(t, map[string]string{"OUTBOX_BATCH_SIZE": "zero"}, "relay")
	if code != exitUsage || !strings.Contains(stderr, "OUTBOX_BATCH_SIZE") {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
}

func TestRunFailsWhenRequiredVariablesAreMissing(t *testing.T) {
	t.Parallel()
	for _, cmd := range []string{"migrate", "api", "relay", "consumer"} {
		code, stdout, _ := runCmd(t, nil, cmd)
		if code != exitError || !strings.Contains(stdout, "missing required environment variables") {
			t.Errorf("%s: code %d log %q", cmd, code, stdout)
		}
	}
}

func TestParseReconcileFlags(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	opts, err := parseReconcileFlags([]string{"-statement", "s.csv", "-from", "2026-11-20", "-to", "2026-11-21", "-format", "json"}, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if !opts.period.From.Equal(time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)) ||
		!opts.period.To.Equal(time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)) || opts.format != "json" {
		t.Fatalf("opts = %+v", opts)
	}

	opts, err = parseReconcileFlags([]string{"-statement", "s.csv", "-from", "2026-11-20"}, &stderr)
	if err != nil || !opts.period.To.Equal(time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("single day: %+v, %v", opts, err)
	}

	for _, args := range [][]string{
		{"-from", "2026-11-20"},
		{"-statement", "s.csv"},
		{"-statement", "s.csv", "-from", "20/11/2026"},
		{"-statement", "s.csv", "-from", "2026-11-20", "-to", "2026-11-19"},
		{"-statement", "s.csv", "-from", "2026-11-20", "-format", "xml"},
	} {
		if _, err := parseReconcileFlags(args, &stderr); err == nil {
			t.Errorf("args %v should fail", args)
		}
	}
}

func TestRunReconcileUsageErrors(t *testing.T) {
	t.Parallel()
	if code, _, _ := runCmd(t, nil, "reconcile", "-from", "2026-11-20"); code != exitUsage {
		t.Fatalf("missing statement: code %d", code)
	}
	code, _, stderr := runCmd(t, nil, "reconcile", "-statement", "x.csv", "-from", "2026-11-20")
	if code != exitUsage || !strings.Contains(stderr, "DATABASE_URL") {
		t.Fatalf("missing database: code %d stderr %q", code, stderr)
	}
	if code, _, _ := runCmd(t, nil, "reconcile", "-h"); code != exitOK {
		t.Fatalf("help: code %d", code)
	}
}

func TestRunHealthcheck(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ok" {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()
	if code, _, _ := runCmd(t, nil, "healthcheck", srv.URL+"/ok"); code != exitOK {
		t.Fatalf("healthy: code %d", code)
	}
	if code, _, _ := runCmd(t, nil, "healthcheck", srv.URL+"/down"); code != exitError {
		t.Fatalf("unhealthy: code %d", code)
	}
	if code, _, _ := runCmd(t, nil, "healthcheck", "http://127.0.0.1:1/"); code != exitError {
		t.Fatalf("unreachable: code %d", code)
	}
	if code, _, _ := runCmd(t, nil, "healthcheck"); code != exitUsage {
		t.Fatalf("no url: code %d", code)
	}
}
