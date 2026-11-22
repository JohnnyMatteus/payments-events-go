package app

import (
	"context"
	"errors"
)

// Erros de autorização, traduzidos pelo adaptador HTTP (401 e 403).
var (
	ErrUnauthenticated = errors.New("no authenticated actor")
	ErrForbidden       = errors.New("role not allowed for this action")
)

// Role é o papel de um operador (ADR 0011 e 0013).
type Role string

// Papéis: o leitor só consulta; o operador também executa ações.
const (
	RoleReader   Role = "leitor"
	RoleOperator Role = "operador"
)

// Valid diz se o papel é um dos conhecidos.
func (r Role) Valid() bool { return r == RoleReader || r == RoleOperator }

// allows diz se o papel r cobre o papel mínimo exigido.
func (r Role) allows(min Role) bool {
	switch min {
	case RoleReader:
		return r == RoleReader || r == RoleOperator
	case RoleOperator:
		return r == RoleOperator
	default:
		return false
	}
}

// Actor é a pessoa autenticada que pede uma operação. Quem o produz é o
// adaptador de entrada (token de dev ou OIDC); o caso de uso só o consome.
type Actor struct {
	Name string
	Role Role
}

type actorKey struct{}

// WithActor devolve um contexto que carrega o ator autenticado.
func WithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, a)
}

// ActorFrom devolve o ator do contexto, se houver.
func ActorFrom(ctx context.Context) (Actor, bool) {
	a, ok := ctx.Value(actorKey{}).(Actor)
	return a, ok
}

// Authorize exige um ator com ao menos o papel min. Falha fechado: sem ator,
// com nome vazio ou com papel desconhecido, a operação não acontece. É a
// barreira de verdade; o middleware HTTP só autentica.
func Authorize(ctx context.Context, min Role) (Actor, error) {
	a, ok := ActorFrom(ctx)
	if !ok || a.Name == "" {
		return Actor{}, ErrUnauthenticated
	}
	if !a.Role.Valid() || !a.Role.allows(min) {
		return Actor{}, ErrForbidden
	}
	return a, nil
}
