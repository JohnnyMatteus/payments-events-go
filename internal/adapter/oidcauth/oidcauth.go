// Package oidcauth autentica operadores por token de acesso OIDC (Keycloak do
// platform-edge). O painel faz o login com PKCE e envia o access token; aqui
// só se verifica assinatura, emissor, audiência e validade, e se traduzem as
// roles do realm nos papéis do serviço. Quem decide a permissão é o caso de
// uso (app.Authorize), não este pacote.
package oidcauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/httpapi"
	"github.com/JohnnyMatteus/payments-events-go/internal/app"
)

// Config descreve o emissor e o mapeamento de roles.
type Config struct {
	IssuerURL string
	// Audience é o "aud" exigido no access token (o Keycloak o põe por mapper).
	Audience string
	// ReaderRole e OperatorRole são roles do realm (realm_access.roles).
	ReaderRole   string
	OperatorRole string
}

// Authenticator valida access tokens de portador.
type Authenticator struct {
	verifier *oidc.IDTokenVerifier
	cfg      Config
}

var _ httpapi.Authenticator = (*Authenticator)(nil)

// New descobre o emissor (JWKS incluso) e monta o verificador. Falha se o
// emissor não responde: sem ele nenhum token seria validado.
func New(ctx context.Context, cfg Config) (*Authenticator, error) {
	if cfg.IssuerURL == "" || cfg.Audience == "" || cfg.ReaderRole == "" || cfg.OperatorRole == "" {
		return nil, errors.New("oidc: issuer, audience e as duas roles são obrigatórios")
	}
	if cfg.ReaderRole == cfg.OperatorRole {
		return nil, errors.New("oidc: role de leitor e de operador devem ser diferentes")
	}
	provider, err := oidc.NewProvider(ctx, cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	return &Authenticator{
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.Audience}),
		cfg:      cfg,
	}, nil
}

type claims struct {
	PreferredUsername string `json:"preferred_username"`
	Subject           string `json:"sub"`
	RealmAccess       struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`
}

// Authenticate verifica o token e devolve o ator. Falha fechado: token
// inválido é ErrUnauthorized; token válido sem role mapeada é ErrNoRole.
func (a *Authenticator) Authenticate(r *http.Request) (app.Actor, error) {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || raw == "" {
		return app.Actor{}, httpapi.ErrUnauthorized
	}
	tok, err := a.verifier.Verify(r.Context(), raw)
	if err != nil {
		return app.Actor{}, httpapi.ErrUnauthorized
	}
	var c claims
	if err := tok.Claims(&c); err != nil {
		return app.Actor{}, httpapi.ErrUnauthorized
	}
	role, ok := a.roleOf(c.RealmAccess.Roles)
	if !ok {
		return app.Actor{}, httpapi.ErrNoRole
	}
	// Sem sub não há identidade estável: falha fechado. O username é só exibição.
	if tok.Issuer == "" || tok.Subject == "" {
		return app.Actor{}, httpapi.ErrUnauthorized
	}
	name := c.PreferredUsername
	if name == "" {
		name = tok.Subject
	}
	return app.Actor{ID: tok.Issuer + "|" + tok.Subject, Name: name, Role: role}, nil
}

// roleOf escolhe o papel mais forte entre as roles do token.
func (a *Authenticator) roleOf(roles []string) (app.Role, bool) {
	var reader bool
	for _, r := range roles {
		switch r {
		case a.cfg.OperatorRole:
			return app.RoleOperator, true
		case a.cfg.ReaderRole:
			reader = true
		}
	}
	if reader {
		return app.RoleReader, true
	}
	return "", false
}
