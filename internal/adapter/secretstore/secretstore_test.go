package secretstore

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// Os testes usam dublês HTTP dos dois serviços: provam o contrato de leitura,
// não a compatibilidade com um Vault ou Secrets Manager reais.

func TestVaultReadsKVv2Field(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Vault-Token") != "dev-token" || r.URL.Path != "/v1/secret/data/payments/webhook" {
			http.Error(w, `{"errors":["denied"]}`, http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"data": map[string]any{"secrets": "novo,antigo", "numero": 7}, "metadata": map[string]any{"version": 3},
		}})
	}))
	defer srv.Close()
	v, err := NewVault(t.Context(), VaultConfig{Addr: srv.URL, Token: "dev-token"})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := v.Get(t.Context(), "payments/webhook#secrets"); err != nil || got != "novo,antigo" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	for _, ref := range []string{"payments/webhook", "payments/webhook#numero", "payments/webhook#ausente", "#x"} {
		if _, err := v.Get(t.Context(), ref); err == nil {
			t.Errorf("ref %q must fail", ref)
		}
	}
	bad, _ := NewVault(t.Context(), VaultConfig{Addr: srv.URL, Token: "outro"})
	if _, err := bad.Get(t.Context(), "payments/webhook#secrets"); err == nil {
		t.Error("token recusado deve falhar")
	}
}

func TestVaultKubernetesLoginRenewsOnForbidden(t *testing.T) {
	var logins atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/kubernetes/login":
			n := logins.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"auth": map[string]any{"client_token": "tok-" + strconv.Itoa(int(n))}})
		case "/v1/secret/data/db":
			if r.Header.Get("X-Vault-Token") != "tok-2" { // o primeiro token "expirou"
				http.Error(w, `{"errors":["permission denied"]}`, http.StatusForbidden)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"data": map[string]any{"password": "s3nha"}}})
		}
	}))
	defer srv.Close()
	jwt := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(jwt, []byte("sa-jwt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	v, err := NewVault(t.Context(), VaultConfig{Addr: srv.URL, Role: "payments", JWTPath: jwt})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := v.Get(t.Context(), "db#password"); err != nil || got != "s3nha" || logins.Load() != 2 {
		t.Fatalf("Get = %q, %v, logins = %d", got, err, logins.Load())
	}
}

func TestVaultRequiresCredentials(t *testing.T) {
	if _, err := NewVault(t.Context(), VaultConfig{Addr: "http://x"}); err == nil {
		t.Error("sem token nem role deve falhar")
	}
	if _, err := NewVault(t.Context(), VaultConfig{Token: "t"}); err == nil {
		t.Error("sem endereço deve falhar")
	}
}

func TestAWSSecretsManagerReadsWholeValueAndJSONField(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Amz-Target") != "secretsmanager.GetSecretValue" {
			http.Error(w, "alvo inesperado", http.StatusBadRequest)
			return
		}
		var in struct {
			SecretID string `json:"SecretId"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		switch in.SecretID {
		case "payments/webhook":
			_ = json.NewEncoder(w).Encode(map[string]any{"Name": in.SecretID, "SecretString": "novo,antigo"})
		case "payments/db":
			_ = json.NewEncoder(w).Encode(map[string]any{"Name": in.SecretID, "SecretString": `{"password":"s3nha"}`})
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"__type":"ResourceNotFoundException","message":"nope"}`))
		}
	}))
	defer srv.Close()
	a, err := NewAWSSecretsManager(t.Context(), AWSConfig{Endpoint: srv.URL, Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := a.Get(t.Context(), "payments/webhook"); err != nil || got != "novo,antigo" {
		t.Fatalf("whole = %q, %v", got, err)
	}
	if got, err := a.Get(t.Context(), "payments/db#password"); err != nil || got != "s3nha" {
		t.Fatalf("field = %q, %v", got, err)
	}
	for _, ref := range []string{"payments/webhook#x", "payments/db#ausente", "nao/existe", ""} {
		if _, err := a.Get(t.Context(), ref); err == nil {
			t.Errorf("ref %q must fail", ref)
		}
	}
	if strings.Contains(srv.URL, "amazonaws") {
		t.Fatal("o teste nunca deve falar com a AWS")
	}
}
