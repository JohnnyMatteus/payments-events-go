package oidcauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/httpapi"
	"github.com/JohnnyMatteus/payments-events-go/internal/app"
	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
)

// idp é um emissor OIDC mínimo (discovery e JWKS) para provar a verificação
// de ponta a ponta sem Keycloak. Não prova compatibilidade com o Keycloak real.
type idp struct {
	srv *httptest.Server
	key *rsa.PrivateKey
}

func newIDP(t *testing.T) *idp {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &idp{key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": p.srv.URL, "jwks_uri": p.srv.URL + "/jwks", "authorization_endpoint": p.srv.URL + "/auth",
			"token_endpoint": p.srv.URL + "/token", "id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
			{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"},
		}})
	})
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

type tokenOpts struct {
	audience string
	roles    []string
	user     string
	sub      *string
	expires  time.Duration
	signer   *rsa.PrivateKey
}

func (p *idp) token(t *testing.T, o tokenOpts) string {
	t.Helper()
	if o.audience == "" {
		o.audience = "edge-gateway"
	}
	if o.expires == 0 {
		o.expires = time.Hour
	}
	if o.signer == nil {
		o.signer = p.key
	}
	sig, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: o.signer},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "k1"))
	if err != nil {
		t.Fatal(err)
	}
	sub := "sub-1"
	if o.sub != nil {
		sub = *o.sub
	}
	now := time.Now()
	raw, err := jwt.Signed(sig).Claims(jwt.Claims{
		Issuer: p.srv.URL, Subject: sub, Audience: jwt.Audience{o.audience},
		IssuedAt: jwt.NewNumericDate(now), Expiry: jwt.NewNumericDate(now.Add(o.expires)),
	}).Claims(map[string]any{
		"preferred_username": o.user, "realm_access": map[string]any{"roles": o.roles},
	}).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func newAuth(t *testing.T, p *idp) *Authenticator {
	t.Helper()
	a, err := New(t.Context(), Config{IssuerURL: p.srv.URL, Audience: "edge-gateway", ReaderRole: "payments.read", OperatorRole: "payments.write"})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func authenticate(a *Authenticator, token string) (app.Actor, error) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return a.Authenticate(req)
}

func TestAuthenticateMapsRealmRolesToRoles(t *testing.T) {
	p := newIDP(t)
	a := newAuth(t, p)
	for name, tc := range map[string]struct {
		roles []string
		want  app.Role
	}{
		"leitor":                  {[]string{"payments.read"}, app.RoleReader},
		"operador":                {[]string{"payments.write"}, app.RoleOperator},
		"operador vence o leitor": {[]string{"payments.read", "payments.write", "outra"}, app.RoleOperator},
	} {
		got, err := authenticate(a, p.token(t, tokenOpts{roles: tc.roles, user: "ana"}))
		if err != nil || got.Name != "ana" || got.Role != tc.want {
			t.Errorf("%s: actor = %+v, err = %v", name, got, err)
		}
	}
}

func TestAuthenticateFailsClosed(t *testing.T) {
	p := newIDP(t)
	a := newAuth(t, p)
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		token string
		want  error
	}{
		"sem token":           {"", httpapi.ErrUnauthorized},
		"lixo":                {"abc.def.ghi", httpapi.ErrUnauthorized},
		"audiência errada":    {p.token(t, tokenOpts{roles: []string{"payments.write"}, user: "ana", audience: "outro"}), httpapi.ErrUnauthorized},
		"expirado":            {p.token(t, tokenOpts{roles: []string{"payments.write"}, user: "ana", expires: -time.Hour}), httpapi.ErrUnauthorized},
		"assinatura de outro": {p.token(t, tokenOpts{roles: []string{"payments.write"}, user: "ana", signer: other}), httpapi.ErrUnauthorized},
		"sem role mapeada":    {p.token(t, tokenOpts{roles: []string{"studios.read"}, user: "ana"}), httpapi.ErrNoRole},
	} {
		if _, err := authenticate(a, tc.token); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
}

func TestNewRejectsBadConfig(t *testing.T) {
	p := newIDP(t)
	for name, cfg := range map[string]Config{
		"vazio":         {},
		"roles iguais":  {IssuerURL: p.srv.URL, Audience: "a", ReaderRole: "x", OperatorRole: "x"},
		"emissor morto": {IssuerURL: "http://127.0.0.1:1", Audience: "a", ReaderRole: "r", OperatorRole: "w"},
	} {
		if _, err := New(t.Context(), cfg); err == nil {
			t.Errorf("%s: New must fail", name)
		}
	}
}

// Ponto de entrada: um leitor autenticado por OIDC recebe 403 numa ação e
// nada é gravado; um operador chega ao caso de uso.
func TestOIDCReaderIsForbiddenOnActionsThroughRouter(t *testing.T) {
	p := newIDP(t)
	a := newAuth(t, p)
	mux := http.NewServeMux()
	deps := httpapi.AdminDeps{
		DeadLetters: app.NewDeadLetterService(nil), // nil: tocar o repositório derruba o teste
		Statements:  app.NewStatementService(nil, nil),
		Runner:      app.NewReconciliationRunner(nil, nil, nil, nil),
	}
	httpapi.NewAdminHandler(deps).Register(mux, a)
	post := func(token string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/dead-letters/broker/1/discard", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := post(p.token(t, tokenOpts{roles: []string{"payments.read"}, user: "carla"})); got != http.StatusForbidden {
		t.Fatalf("leitor: status = %d, want 403", got)
	}
	if got := post(p.token(t, tokenOpts{roles: []string{"studios.read"}, user: "x"})); got != http.StatusForbidden {
		t.Fatalf("sem role: status = %d, want 403", got)
	}
	if got := post("lixo"); got != http.StatusUnauthorized {
		t.Fatalf("token inválido: status = %d, want 401", got)
	}
}

type stubCases struct {
	app.CaseRepository
	adjustment app.Adjustment
	approved   bool
}

func (s *stubCases) ApproveAdjustment(_ context.Context, _ int64, decide func(app.Adjustment) (app.Recording, error),
	_ app.Actor, _ string) (app.Adjustment, error) {
	if _, err := decide(s.adjustment); err != nil {
		return app.Adjustment{}, err
	}
	s.approved = true
	return s.adjustment, nil
}

// Quatro olhos pela identidade estável: quem propôs não aprova mesmo trocando
// o nome de usuário e entrando de novo; sem sub o token nem autentica.
func TestFourEyesUsesIssuerAndSubNotUsername(t *testing.T) {
	p := newIDP(t)
	a := newAuth(t, p)
	cases := &stubCases{adjustment: app.Adjustment{
		ID: 1, Status: domain.AdjustmentProposed, ProposedBy: "ana", ProposedByID: p.srv.URL + "|sub-ana",
	}}
	mux := http.NewServeMux()
	httpapi.NewAdminHandler(httpapi.AdminDeps{
		CaseService: app.NewCaseService(cases), Cases: cases, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}).Register(mux, a)
	approve := func(token string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/adjustments/1/approve", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}
	str := func(v string) *string { return &v }
	renamed := p.token(t, tokenOpts{roles: []string{"payments.write"}, user: "ana.renomeada", sub: str("sub-ana")})
	if got := approve(renamed); got != http.StatusForbidden || cases.approved {
		t.Fatalf("mesmo sub com outro username: status = %d approved = %v, want 403 e nada aprovado", got, cases.approved)
	}
	namesake := p.token(t, tokenOpts{roles: []string{"payments.write"}, user: "ana", sub: str("sub-outra-pessoa")})
	if got := approve(namesake); got != http.StatusOK || !cases.approved {
		t.Fatalf("outra pessoa com o mesmo username: status = %d, want 200", got)
	}
	cases.approved = false
	if got := approve(p.token(t, tokenOpts{roles: []string{"payments.write"}, user: "ana", sub: str("")})); got != http.StatusUnauthorized || cases.approved {
		t.Fatalf("sem sub: status = %d, want 401", got)
	}
}
