package domain_test

import (
	"errors"
	"testing"

	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
)

func TestParseAmountCents(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{in: "150.00", want: 15000},
		{in: "0.5", want: 50},
		{in: "42", want: 4200},
		{in: " 7.07 ", want: 707},
		{in: "0.01", want: 1},
		{in: "92233720368547758.07", want: 9223372036854775807},
		{in: "92233720368547758.08", wantErr: true},
		{in: "", wantErr: true},
		{in: "-1.00", wantErr: true},
		{in: "1.234", wantErr: true},
		{in: "1.", wantErr: true},
		{in: ".50", wantErr: true},
		{in: "1,50", wantErr: true},
		{in: "1e3", wantErr: true},
		{in: "+1.00", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, err := domain.ParseAmountCents(tt.in)
			if tt.wantErr {
				if !errors.Is(err, domain.ErrInvalidAmount) {
					t.Fatalf("ParseAmountCents(%q) err = %v, want ErrInvalidAmount", tt.in, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseAmountCents(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("ParseAmountCents(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

// FuzzParseAmountCents garante que qualquer valor aceito volta ao mesmo número
// quando formatado e analisado de novo, e que nunca há valor negativo.
func FuzzParseAmountCents(f *testing.F) {
	for _, seed := range []string{"0", "0.01", "150.00", "1.5", "999999999.99", "-1", "1.234", "abc"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		cents, err := domain.ParseAmountCents(in)
		if err != nil {
			return
		}
		if cents < 0 {
			t.Fatalf("negative amount %d for %q", cents, in)
		}
		again, err := domain.ParseAmountCents(domain.FormatCents(cents))
		if err != nil || again != cents {
			t.Fatalf("round trip failed for %q: %d -> %q -> %d (%v)", in, cents, domain.FormatCents(cents), again, err)
		}
	})
}

func TestFormatCents(t *testing.T) {
	t.Parallel()
	cases := map[int64]string{0: "0.00", 1: "0.01", 15000: "150.00", -2550: "-25.50"}
	for in, want := range cases {
		if got := domain.FormatCents(in); got != want {
			t.Errorf("FormatCents(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestParseCurrency(t *testing.T) {
	t.Parallel()
	if got, err := domain.ParseCurrency(" brl "); err != nil || got != "BRL" {
		t.Fatalf("ParseCurrency(brl) = %q, %v", got, err)
	}
	for _, bad := range []string{"", "BR", "BRLL", "B1L", "R$ "} {
		if _, err := domain.ParseCurrency(bad); !errors.Is(err, domain.ErrInvalidCurrency) {
			t.Errorf("ParseCurrency(%q) err = %v, want ErrInvalidCurrency", bad, err)
		}
	}
}
