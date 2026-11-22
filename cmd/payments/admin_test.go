package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/httpapi"
	"github.com/JohnnyMatteus/payments-events-go/internal/platform/config"
)

// Em produção o token estático de dev não pode autenticar operador, e os dois
// modos juntos são ambíguos: o processo recusa subir.
func TestOperatorAuthenticatorModes(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tokens := []string{"ana:operador:dev-token-ana"}
	for name, cfg := range map[string]config.Config{
		"token em produção":      {AppEnv: "production", OperatorTokens: tokens},
		"oidc e token juntos":    {AppEnv: "dev", OIDCIssuerURL: "http://idp.invalid", OperatorTokens: tokens},
		"oidc com emissor morto": {AppEnv: "production", OIDCIssuerURL: "http://127.0.0.1:1", OIDCAudience: "a", OIDCRoleReader: "r", OIDCRoleOperator: "w"},
	} {
		if auth, err := operatorAuthenticator(t.Context(), cfg, logger); err == nil || auth != nil {
			t.Errorf("%s: want error, got auth=%v err=%v", name, auth, err)
		}
	}
	if auth, err := operatorAuthenticator(t.Context(), config.Config{AppEnv: "dev", OperatorTokens: tokens}, logger); err != nil || auth == nil {
		t.Errorf("token em dev: auth=%v err=%v", auth, err)
	}
	if auth, err := operatorAuthenticator(t.Context(), config.Config{AppEnv: "production"}, logger); err != nil || auth != nil {
		t.Errorf("sem autenticação: API de operação deve ficar desligada, auth=%v err=%v", auth, err)
	}
}

// Rotação pelo ponto de entrada da composição: os segredos vêm do Vault (dublê),
// o cofre passa a devolver "novo,antigo" e depois só "novo", e o verificador
// montado por newVerifier acompanha sem reinício.
func TestNewVerifierFollowsVaultRotation(t *testing.T) {
	var current atomic.Value
	current.Store("whsec_old")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"data": map[string]any{"secrets": current.Load().(string)},
		}})
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cfg := config.Config{
		SecretsProvider: "vault", VaultAddr: srv.URL, VaultToken: "dev", VaultKVMount: "secret",
		WebhookSecretsRef: "payments/webhook#secrets", SignatureTolerance: time.Minute,
		SecretsRefreshInterval: 10 * time.Millisecond,
	}
	v, err := newVerifier(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{}`)
	verify := func(secret string) error { return v.Verify(httpapi.Sign([]byte(secret), time.Now(), body), body) }
	eventually := func(what string, cond func() bool) {
		t.Helper()
		for range 200 {
			if cond() {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("timeout: %s", what)
	}
	if verify("whsec_old") != nil || verify("whsec_new") == nil {
		t.Fatal("antes da rotação só a chave antiga vale")
	}
	current.Store("whsec_new,whsec_old")
	eventually("as duas chaves validam", func() bool { return verify("whsec_new") == nil && verify("whsec_old") == nil })
	current.Store("whsec_new")
	eventually("a chave antiga sai", func() bool { return verify("whsec_old") != nil && verify("whsec_new") == nil })
	// Cofre vazio ou fora do ar: o último valor bom segue valendo.
	current.Store("")
	time.Sleep(60 * time.Millisecond)
	if verify("whsec_new") != nil {
		t.Fatal("segredo vazio no cofre não pode derrubar a verificação")
	}
}

func TestNewVerifierFailsClosedWithoutSecrets(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := newVerifier(t.Context(), config.Config{SignatureTolerance: time.Minute}, logger); err == nil {
		t.Error("sem WEBHOOK_SECRETS nem ref deve falhar")
	}
	dead := config.Config{SecretsProvider: "vault", VaultAddr: "http://127.0.0.1:1", VaultToken: "t",
		WebhookSecretsRef: "a#b", SignatureTolerance: time.Minute}
	if _, err := newVerifier(t.Context(), dead, logger); err == nil {
		t.Error("cofre fora do ar na subida deve falhar")
	}
}
