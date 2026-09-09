package strategy

import (
	"math"
	"testing"
)

// roundMoney8 must never emit IEEE-754 negative zero. Arithmetic does not care
// (-0 == 0), but "-0" is what a mysqldump writes and +0 is what MySQL's parser
// reads back, so a -0 in the DB is a permanent phantom diff in any byte- or
// text-level restore check (台账 #155).
func TestRoundMoney8NeverEmitsNegativeZero(t *testing.T) {
	// The exact residue that produced the one -0 row on prod, id=1721:
	// 3990 * (0.015569999999999999 - 0.01557).
	residue := 3990 * (0.015569999999999999 - 0.01557)
	if residue >= 0 {
		t.Fatalf("fixture no longer negative: %g", residue)
	}
	cases := []float64{residue, -1e-12, -0.0000000049, math.Copysign(0, -1)}
	for _, in := range cases {
		got := roundMoney8(in)
		if got != 0 {
			t.Fatalf("roundMoney8(%g) = %v, want 0", in, got)
		}
		if math.Signbit(got) {
			t.Errorf("roundMoney8(%g) returned negative zero", in)
		}
	}
}

func TestRoundMoney8KeepsRealValues(t *testing.T) {
	cases := map[float64]float64{
		2.579356:      2.579356,
		-0.628916:     -0.628916,
		0.123456789:   0.12345679,
		-0.000000005:  -0.00000001, // still a real, signed value after rounding
		0.41236000004: 0.41236,
	}
	for in, want := range cases {
		if got := roundMoney8(in); got != want {
			t.Errorf("roundMoney8(%g) = %v, want %v", in, got, want)
		}
	}
	if got := roundMoney8(math.Inf(-1)); !math.IsInf(got, -1) {
		t.Errorf("roundMoney8(-Inf) = %v, want -Inf", got)
	}
	if got := roundMoney8(math.NaN()); !math.IsNaN(got) {
		t.Errorf("roundMoney8(NaN) = %v, want NaN", got)
	}
}
