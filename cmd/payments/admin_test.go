package main

import (
	"io"
	"log/slog"
	"testing"

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
