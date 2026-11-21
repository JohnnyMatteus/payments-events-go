package app

import (
	"math/rand/v2"
	"time"
)

// Backoff calcula o atraso exponencial entre tentativas: Base * 2^(n-1),
// limitado a Max. Jitter, quando definido, espalha as tentativas para evitar
// que muitas mensagens voltem ao broker no mesmo instante.
type Backoff struct {
	Base   time.Duration
	Max    time.Duration
	Jitter func(time.Duration) time.Duration
}

// Delay devolve o atraso antes da tentativa de número attempt (1, 2, 3...).
func (b Backoff) Delay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := b.Base
	for i := 1; i < attempt && d < b.Max; i++ {
		d *= 2
	}
	if d > b.Max || d <= 0 {
		d = b.Max
	}
	if b.Jitter != nil {
		d = b.Jitter(d)
	}
	return d
}

// EqualJitter mantém metade do atraso e sorteia a outra metade, de modo que
// o atraso nunca cai abaixo de d/2 nem passa de d.
func EqualJitter(d time.Duration) time.Duration {
	half := d / 2
	if half <= 0 {
		return d
	}
	return half + rand.N(half+1) //nolint:gosec // jitter não exige aleatoriedade criptográfica
}
