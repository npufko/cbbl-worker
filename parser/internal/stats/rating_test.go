package stats

import (
	"math"
	"testing"
)

func TestRating2ApproxAverageIsNearOne(t *testing.T) {
	// A roughly average line: 0.68 KPR, 0.68 DPR, 0.13 APR, 72% KAST, 76 ADR over 24 rounds.
	got := Rating2Approx(16, 16, 3, 24, 0.72, 76)
	if math.Abs(got-1.0) > 0.08 {
		t.Fatalf("average line should rate ≈1.00, got %.3f", got)
	}
}

func TestRating2ApproxOrdering(t *testing.T) {
	strong := Rating2Approx(24, 15, 6, 22, 0.77, 98)
	weak := Rating2Approx(11, 15, 9, 22, 0.73, 58)
	if !(strong > 1.2 && weak < 0.95 && strong > weak) {
		t.Fatalf("unexpected ordering strong=%.2f weak=%.2f", strong, weak)
	}
}

func TestZeroRounds(t *testing.T) {
	if Rating2Approx(1, 1, 1, 0, 1, 1) != 0 {
		t.Fatal("zero rounds must rate 0")
	}
}
