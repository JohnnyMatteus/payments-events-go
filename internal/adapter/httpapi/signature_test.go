package httpapi

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var sigNow = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

func newTestVerifier(t *testing.T, secrets ...string) *Verifier {
	t.Helper()
	v, err := NewVerifier(secrets, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	v.now = func() time.Time { return sigNow }
	return v
}

func TestVerifyAcceptsValidSignature(t *testing.T) {
	t.Parallel()
	body := []byte(`{"id":"evt_1"}`)
	header := Sign([]byte("whsec_test"), sigNow.Add(-time.Minute), body)
	if err := newTestVerifier(t, "whsec_test").Verify(header, body); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestVerifyRejections(t *testing.T) {
	t.Parallel()
	secret := []byte("whsec_test")
	body := []byte(`{"id":"evt_1","amount":100}`)
	valid := Sign(secret, sigNow, body)
	ts := valid[:strings.Index(valid, ",")]

	tests := []struct {
		name   string
		header string
		body   []byte
		want   error
	}{
		{name: "sem header", header: "", body: body, want: ErrMissingSignature},
		{name: "corpo adulterado", header: valid, body: []byte(`{"id":"evt_1","amount":999}`), want: ErrSignatureMismatch},
		{name: "segredo errado", header: Sign([]byte("other"), sigNow, body), body: body, want: ErrSignatureMismatch},
		{name: "replay antigo", header: Sign(secret, sigNow.Add(-6*time.Minute), body), body: body, want: ErrStaleTimestamp},
		{name: "timestamp no futuro", header: Sign(secret, sigNow.Add(6*time.Minute), body), body: body, want: ErrStaleTimestamp},
		{name: "sem timestamp", header: "v1=" + strings.Repeat("a", 64), body: body, want: ErrMalformedSignature},
		{name: "sem v1", header: ts, body: body, want: ErrMalformedSignature},
		{name: "timestamp não numérico", header: "t=abc,v1=" + strings.Repeat("a", 64), body: body, want: ErrMalformedSignature},
		{name: "timestamp duplicado", header: ts + "," + valid, body: body, want: ErrMalformedSignature},
		{name: "hex inválido", header: ts + ",v1=zz", body: body, want: ErrMalformedSignature},
		{name: "tamanho errado", header: ts + ",v1=abcd", body: body, want: ErrMalformedSignature},
		{name: "parte sem igual", header: ts + ",lixo", body: body, want: ErrMalformedSignature},
	}
	v := newTestVerifier(t, string(secret))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := v.Verify(tt.header, tt.body); !errors.Is(err, tt.want) {
				t.Fatalf("Verify err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestVerifyToleranceBoundaryIsInclusive(t *testing.T) {
	t.Parallel()
	body := []byte(`{}`)
	v := newTestVerifier(t, "s")
	if err := v.Verify(Sign([]byte("s"), sigNow.Add(-5*time.Minute), body), body); err != nil {
		t.Fatalf("exactly at tolerance should pass: %v", err)
	}
}

func TestVerifySupportsSecretRotation(t *testing.T) {
	t.Parallel()
	body := []byte(`{"id":"evt_rot"}`)
	v := newTestVerifier(t, "new-secret", "old-secret")
	if err := v.Verify(Sign([]byte("old-secret"), sigNow, body), body); err != nil {
		t.Fatalf("old secret during rotation: %v", err)
	}
	// O provedor pode mandar várias assinaturas; basta uma bater.
	header := Sign([]byte("unknown"), sigNow, body) + "," + strings.SplitN(Sign([]byte("new-secret"), sigNow, body), ",", 2)[1]
	if err := v.Verify(header, body); err != nil {
		t.Fatalf("multiple v1 values: %v", err)
	}
}

func TestVerifyIgnoresUnknownSchemes(t *testing.T) {
	t.Parallel()
	body := []byte(`{}`)
	header := Sign([]byte("s"), sigNow, body) + ",v0=legacy"
	if err := newTestVerifier(t, "s").Verify(header, body); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestNewVerifierValidation(t *testing.T) {
	t.Parallel()
	if _, err := NewVerifier([]string{" ", ""}, time.Minute); err == nil {
		t.Fatal("blank secrets must be rejected")
	}
	if _, err := NewVerifier([]string{"s"}, 0); err == nil {
		t.Fatal("zero tolerance must be rejected")
	}
}

func BenchmarkVerify(b *testing.B) {
	body := []byte(strings.Repeat(`{"id":"evt_1","data":{"amount":15000}}`, 20))
	v, _ := NewVerifier([]string{"whsec_bench"}, 5*time.Minute)
	header := Sign([]byte("whsec_bench"), time.Now(), body)
	b.ReportAllocs()
	for b.Loop() {
		if err := v.Verify(header, body); err != nil {
			b.Fatal(err)
		}
	}
}
