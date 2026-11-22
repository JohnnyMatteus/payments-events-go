package app

import (
	"context"
	"errors"
	"testing"
)

type scriptedSecrets struct {
	values []string
	errs   []error
	calls  int
}

func (s *scriptedSecrets) Get(context.Context, string) (string, error) {
	i := s.calls
	s.calls++
	return s.values[i], s.errs[i]
}

func TestSecretRotationKeepsLastGoodValue(t *testing.T) {
	store := &scriptedSecrets{
		values: []string{"v1", "", "v1", "", "v2"},
		errs:   []error{nil, nil, nil, errors.New("vault fora"), nil},
	}
	var applied []string
	r := NewSecretRotation(store, "ref", func(v string) error { applied = append(applied, v); return nil }, discardLogger())
	if err := r.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := r.refresh(t.Context()); !errors.Is(err, ErrEmptySecret) {
		t.Fatalf("empty secret = %v, want ErrEmptySecret", err)
	}
	if err := r.refresh(t.Context()); err != nil { // mesmo valor: não reaplica
		t.Fatal(err)
	}
	if err := r.refresh(t.Context()); err == nil { // falha de leitura
		t.Fatal("read failure must surface")
	}
	if err := r.refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(applied) != 2 || applied[0] != "v1" || applied[1] != "v2" {
		t.Fatalf("applied = %v, want [v1 v2]", applied)
	}
}

func TestSecretRotationLoadFailsClosed(t *testing.T) {
	store := &scriptedSecrets{values: []string{""}, errs: []error{errors.New("sem acesso")}}
	r := NewSecretRotation(store, "ref", func(string) error { t.Fatal("must not apply"); return nil }, discardLogger())
	if err := r.Load(t.Context()); err == nil {
		t.Fatal("startup must fail when the secret cannot be read")
	}
}
