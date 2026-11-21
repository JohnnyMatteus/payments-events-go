package domain

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ErrInvalidAmount indica um valor monetário mal formado.
var ErrInvalidAmount = errors.New("invalid amount")

// ErrInvalidCurrency indica um código de moeda fora do padrão ISO 4217.
var ErrInvalidCurrency = errors.New("invalid currency")

// Currency é um código de moeda ISO 4217 (três letras maiúsculas).
type Currency string

// ParseCurrency valida e normaliza um código de moeda.
func ParseCurrency(s string) (Currency, error) {
	c := strings.ToUpper(strings.TrimSpace(s))
	if len(c) != 3 {
		return "", fmt.Errorf("%w: %q", ErrInvalidCurrency, s)
	}
	for _, r := range c {
		if r < 'A' || r > 'Z' {
			return "", fmt.Errorf("%w: %q", ErrInvalidCurrency, s)
		}
	}
	return Currency(c), nil
}

// ParseAmountCents converte um valor decimal com ponto ("150.00", "0.5", "42")
// em centavos, sem passar por ponto flutuante. Valores negativos, mais de duas
// casas decimais e estouro de int64 são rejeitados.
func ParseAmountCents(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("%w: empty", ErrInvalidAmount)
	}
	whole, frac, hasDot := strings.Cut(s, ".")
	if whole == "" || (hasDot && (frac == "" || len(frac) > 2)) {
		return 0, fmt.Errorf("%w: %q", ErrInvalidAmount, s)
	}
	if !isDigits(whole) || !isDigits(frac) {
		return 0, fmt.Errorf("%w: %q", ErrInvalidAmount, s)
	}
	units, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || units > math.MaxInt64/100 {
		return 0, fmt.Errorf("%w: %q out of range", ErrInvalidAmount, s)
	}
	cents := int64(0)
	if frac != "" {
		if len(frac) == 1 {
			frac += "0"
		}
		// frac tem exatamente dois dígitos validados acima.
		cents, _ = strconv.ParseInt(frac, 10, 64)
	}
	total := units*100 + cents
	if total < 0 {
		return 0, fmt.Errorf("%w: %q out of range", ErrInvalidAmount, s)
	}
	return total, nil
}

// FormatCents formata centavos como decimal com duas casas ("150.00").
func FormatCents(cents int64) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s%d.%02d", sign, cents/100, cents%100)
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
