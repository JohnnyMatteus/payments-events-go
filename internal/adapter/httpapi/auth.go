package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/JohnnyMatteus/payments-events-go/internal/app"
)

// Role é o papel do operador (ADR 0011); vive em app porque quem decide é o caso de uso.
type Role = app.Role

// Papéis aceitos.
const (
	RoleReader   = app.RoleReader
	RoleOperator = app.RoleOperator
)

// Erros de autenticação. O middleware os traduz em 401 e 403.
var (
	// ErrUnauthorized: sem credencial ou credencial inválida.
	ErrUnauthorized = errors.New("unauthorized")
	// ErrNoRole: credencial válida, mas sem papel mapeado para este serviço.
	ErrNoRole = errors.New("authenticated without a known role")
)

// Authenticator identifica quem fez a requisição. Token estático (dev) e OIDC
// o implementam; a autorização por papel não mora aqui, mora no caso de uso.
type Authenticator interface {
	Authenticate(r *http.Request) (app.Actor, error)
}

// OperatorFrom devolve o operador autenticado da requisição.
func OperatorFrom(ctx context.Context) string {
	a, _ := app.ActorFrom(ctx)
	return a.Name
}

// RoleFrom devolve o papel do operador autenticado.
func RoleFrom(ctx context.Context) Role {
	a, _ := app.ActorFrom(ctx)
	return a.Role
}

// Authenticated exige credencial válida e põe o ator no contexto. Não decide
// permissão de ação: isso é do caso de uso (app.Authorize).
func Authenticated(a Authenticator, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor, err := a.Authenticate(r)
		switch {
		case errors.Is(err, ErrNoRole):
			writeProblem(w, http.StatusForbidden, "forbidden", "a identidade não tem papel neste serviço")
			return
		case err != nil:
			w.Header().Set("WWW-Authenticate", `Bearer realm="payments-events"`)
			writeProblem(w, http.StatusUnauthorized, "unauthorized", "informe um token válido")
			return
		}
		next.ServeHTTP(w, r.WithContext(app.WithActor(r.Context(), actor)))
	})
}

// OperatorAuth identifica operadores por token de portador. Cada token
// pertence a uma pessoa: a regra dos quatro olhos depende de identidades
// distintas (ADR 0011).
type OperatorAuth struct {
	tokens []operatorToken
}

type operatorToken struct {
	operator string
	role     Role
	digest   [sha256.Size]byte
}

// NewOperatorAuth lê entradas "operador:papel:token" (papel leitor ou
// operador; o token pode conter ":"). Recusa operador repetido, papel
// desconhecido e token curto, para um valor de exemplo não virar credencial
// por descuido.
func NewOperatorAuth(entries []string) (*OperatorAuth, error) {
	a := &OperatorAuth{}
	seen := map[string]bool{}
	for _, e := range entries {
		op, rest, ok := strings.Cut(e, ":")
		op = strings.TrimSpace(op)
		role, token, ok2 := strings.Cut(rest, ":")
		if !ok || !ok2 || op == "" || len(token) < 8 {
			return nil, errors.New("OPERATOR_TOKENS: use operador:papel:token com token de 8 ou mais caracteres")
		}
		if !Role(role).Valid() {
			return nil, fmt.Errorf("OPERATOR_TOKENS: papel %q de %q inválido (use leitor ou operador)", role, op)
		}
		if seen[strings.ToLower(op)] {
			return nil, fmt.Errorf("OPERATOR_TOKENS: operador %q repetido", op)
		}
		seen[strings.ToLower(op)] = true
		a.tokens = append(a.tokens, operatorToken{operator: op, role: Role(role), digest: sha256.Sum256([]byte(token))})
	}
	if len(a.tokens) == 0 {
		return nil, errors.New("OPERATOR_TOKENS: informe ao menos um operador")
	}
	return a, nil
}

func (a *OperatorAuth) operatorFor(token string) (operatorToken, bool) {
	digest := sha256.Sum256([]byte(token))
	var found operatorToken
	for _, t := range a.tokens {
		// Percorre todos para o tempo não revelar qual operador existe.
		if subtle.ConstantTimeCompare(digest[:], t.digest[:]) == 1 {
			found = t
		}
	}
	return found, found.operator != ""
}

// Authenticate lê o token de portador e devolve o operador dono dele.
func (a *OperatorAuth) Authenticate(r *http.Request) (app.Actor, error) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return app.Actor{}, ErrUnauthorized
	}
	who, ok := a.operatorFor(token)
	if !ok {
		return app.Actor{}, ErrUnauthorized
	}
	// Em dev a identidade é o próprio operador do token; o prefixo evita colidir com um ID de OIDC.
	return app.Actor{ID: "dev:" + strings.ToLower(who.operator), Name: who.operator, Role: who.role}, nil
}
