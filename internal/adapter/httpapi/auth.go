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

type operatorKey struct{}

// OperatorFrom devolve o operador autenticado da requisição.
func OperatorFrom(ctx context.Context) string {
	op, _ := ctx.Value(operatorKey{}).(string)
	return op
}

// OperatorAuth identifica operadores por token de portador. Cada token
// pertence a uma pessoa: a regra dos quatro olhos depende de identidades
// distintas (ADR 0011).
type OperatorAuth struct {
	tokens []operatorToken
}

type operatorToken struct {
	operator string
	digest   [sha256.Size]byte
}

// NewOperatorAuth lê entradas "operador:token". Recusa operador repetido e
// token curto, para um valor de exemplo não virar credencial por descuido.
func NewOperatorAuth(entries []string) (*OperatorAuth, error) {
	a := &OperatorAuth{}
	seen := map[string]bool{}
	for _, e := range entries {
		op, token, ok := strings.Cut(e, ":")
		op = strings.TrimSpace(op)
		if !ok || op == "" || len(token) < 8 {
			return nil, errors.New("OPERATOR_TOKENS: use operador:token com token de 8 ou mais caracteres")
		}
		if seen[strings.ToLower(op)] {
			return nil, fmt.Errorf("OPERATOR_TOKENS: operador %q repetido", op)
		}
		seen[strings.ToLower(op)] = true
		a.tokens = append(a.tokens, operatorToken{operator: op, digest: sha256.Sum256([]byte(token))})
	}
	if len(a.tokens) == 0 {
		return nil, errors.New("OPERATOR_TOKENS: informe ao menos um operador")
	}
	return a, nil
}

func (a *OperatorAuth) operatorFor(token string) (string, bool) {
	digest := sha256.Sum256([]byte(token))
	found := ""
	for _, t := range a.tokens {
		// Percorre todos para o tempo não revelar qual operador existe.
		if subtle.ConstantTimeCompare(digest[:], t.digest[:]) == 1 {
			found = t.operator
		}
	}
	return found, found != ""
}

// Middleware exige o token e coloca o operador no contexto.
func (a *OperatorAuth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="payments-events"`)
			writeProblem(w, http.StatusUnauthorized, "unauthorized", "informe o token do operador")
			return
		}
		op, ok := a.operatorFor(token)
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="payments-events", error="invalid_token"`)
			writeProblem(w, http.StatusUnauthorized, "unauthorized", "token inválido")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), operatorKey{}, op)))
	})
}
