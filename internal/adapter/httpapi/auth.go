package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

type (
	operatorKey struct{}
	roleKey     struct{}
)

// Role é o papel do operador (ADR 0011).
type Role string

// Papéis: o leitor só consulta; o operador também executa ações.
const (
	RoleReader   Role = "leitor"
	RoleOperator Role = "operador"
)

func (r Role) valid() bool { return r == RoleReader || r == RoleOperator }

// OperatorFrom devolve o operador autenticado da requisição.
func OperatorFrom(ctx context.Context) string {
	op, _ := ctx.Value(operatorKey{}).(string)
	return op
}

// RoleFrom devolve o papel do operador autenticado.
func RoleFrom(ctx context.Context) Role {
	role, _ := ctx.Value(roleKey{}).(Role)
	return role
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
		if !Role(role).valid() {
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

// Middleware exige o token e coloca o operador e o papel no contexto.
func (a *OperatorAuth) Middleware(next http.Handler) http.Handler {
	return a.require(RoleReader, next)
}

// RequireRole exige o token e um papel mínimo; o leitor recebe 403 nas ações.
func (a *OperatorAuth) RequireRole(min Role, next http.Handler) http.Handler {
	return a.require(min, next)
}

func (a *OperatorAuth) require(min Role, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="payments-events"`)
			writeProblem(w, http.StatusUnauthorized, "unauthorized", "informe o token do operador")
			return
		}
		who, ok := a.operatorFor(token)
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="payments-events", error="invalid_token"`)
			writeProblem(w, http.StatusUnauthorized, "unauthorized", "token inválido")
			return
		}
		if min == RoleOperator && who.role != RoleOperator {
			writeProblem(w, http.StatusForbidden, "forbidden", "o papel leitor só consulta; ação exige o papel operador")
			return
		}
		ctx := context.WithValue(r.Context(), operatorKey{}, who.operator)
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, roleKey{}, who.role)))
	})
}
