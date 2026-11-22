package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// SignatureHeader é o header em que o provedor PagaFácil Sandbox envia a assinatura.
// Formato: "t=<unix seconds>,v1=<hex hmac-sha256>". Pode haver mais de um v1
// durante a rotação de segredo do lado do provedor.
const SignatureHeader = "PagaFacil-Signature"

// Erros de verificação. Todos resultam em 401 sem detalhar a causa ao cliente.
var (
	ErrMissingSignature   = errors.New("missing signature header")
	ErrMalformedSignature = errors.New("malformed signature header")
	ErrStaleTimestamp     = errors.New("timestamp outside tolerance window")
	ErrSignatureMismatch  = errors.New("signature mismatch")
)

// Verifier valida a assinatura HMAC-SHA256 de "<timestamp>.<corpo>" e rejeita
// timestamps fora da janela de tolerância, o que limita ataques de replay.
// Aceita vários segredos para permitir rotação sem janela de indisponibilidade.
type Verifier struct {
	secrets   atomic.Pointer[[][]byte]
	tolerance time.Duration
	now       func() time.Time
}

// NewVerifier cria o verificador. É preciso ao menos um segredo não vazio.
func NewVerifier(secrets []string, tolerance time.Duration) (*Verifier, error) {
	v := &Verifier{tolerance: tolerance, now: time.Now}
	if err := v.SetSecrets(secrets); err != nil {
		return nil, err
	}
	if tolerance <= 0 {
		return nil, errors.New("signature tolerance must be positive")
	}
	return v, nil
}

// SetSecrets troca o conjunto de segredos aceitos, sem parar o serviço. Na
// rotação o conjunto tem o novo e o antigo: ambos validam até o provedor
// terminar de migrar; depois o antigo sai. Um conjunto vazio é recusado e o
// anterior continua valendo (falha fechado, sem aceitar tudo nem nada).
func (v *Verifier) SetSecrets(secrets []string) error {
	var next [][]byte
	for _, s := range secrets {
		if s = strings.TrimSpace(s); s != "" {
			next = append(next, []byte(s))
		}
	}
	if len(next) == 0 {
		return errors.New("at least one webhook secret is required")
	}
	v.secrets.Store(&next)
	return nil
}

// ParseSecretList lê a lista "atual,anterior,..." que o cofre guarda.
func ParseSecretList(raw string) []string {
	var out []string
	for item := range strings.SplitSeq(raw, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// Verify valida o header de assinatura contra o corpo bruto da requisição.
func (v *Verifier) Verify(header string, body []byte) error {
	if header == "" {
		return ErrMissingSignature
	}
	ts, signatures, err := parseSignatureHeader(header)
	if err != nil {
		return err
	}
	age := v.now().Sub(time.Unix(ts, 0))
	if age > v.tolerance || age < -v.tolerance {
		return fmt.Errorf("%w: age %s", ErrStaleTimestamp, age.Round(time.Second))
	}
	for _, secret := range *v.secrets.Load() {
		expected := computeMAC(secret, ts, body)
		for _, sig := range signatures {
			if hmac.Equal(expected, sig) {
				return nil
			}
		}
	}
	return ErrSignatureMismatch
}

// Sign produz o valor do header de assinatura. É o que o provedor faz do lado
// dele; aqui serve para testes e para o script de simulação local.
func Sign(secret []byte, ts time.Time, body []byte) string {
	return fmt.Sprintf("t=%d,v1=%s", ts.Unix(), hex.EncodeToString(computeMAC(secret, ts.Unix(), body)))
}

func computeMAC(secret []byte, ts int64, body []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(strconv.FormatInt(ts, 10)))
	mac.Write([]byte("."))
	mac.Write(body)
	return mac.Sum(nil)
}

func parseSignatureHeader(header string) (int64, [][]byte, error) {
	var (
		ts         int64
		haveTS     bool
		signatures [][]byte
	)
	for part := range strings.SplitSeq(header, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return 0, nil, ErrMalformedSignature
		}
		switch key {
		case "t":
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil || haveTS {
				return 0, nil, ErrMalformedSignature
			}
			ts, haveTS = parsed, true
		case "v1":
			sig, err := hex.DecodeString(value)
			if err != nil || len(sig) != sha256.Size {
				return 0, nil, ErrMalformedSignature
			}
			signatures = append(signatures, sig)
		default:
			// Esquemas desconhecidos (v0, v2...) são ignorados para manter
			// compatibilidade com evoluções do provedor.
		}
	}
	if !haveTS || len(signatures) == 0 {
		return 0, nil, ErrMalformedSignature
	}
	return ts, signatures, nil
}
