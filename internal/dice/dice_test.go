package dice

import (
	"strings"
	"testing"
)

func TestParseExpr(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		average float64
		wantErr bool
	}{
		{name: "single die", in: "1d6", want: "1d6", average: 3.5},
		{name: "implicit count", in: "d8", want: "1d8", average: 4.5},
		{name: "dice and modifier", in: "2d6+3", want: "2d6+3", average: 10},
		{name: "two dice groups", in: "3d6+6d6", want: "3d6+6d6", average: 31.5},
		{name: "negative modifier", in: "1d4-1", want: "1d4-1", average: 1.5},
		{name: "spaces and case", in: " 2D10 + 4 ", want: "2d10+4", average: 15},
		{name: "flat only", in: "7", want: "7", average: 7},
		{name: "empty is not an error", in: "", want: "0", average: 0},
		{name: "garbage", in: "big hit", wantErr: true},
		{name: "bad sides", in: "2dx", wantErr: true},
		{name: "zero dice", in: "0d6", wantErr: true},
		{name: "absurd count", in: "500d6", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseExpr(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseExpr(%q) = %v, want an error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseExpr(%q) returned %v", tc.in, err)
			}
			if got.String() != tc.want {
				t.Errorf("ParseExpr(%q).String() = %q, want %q", tc.in, got.String(), tc.want)
			}
			if got.Average() != tc.average {
				t.Errorf("ParseExpr(%q).Average() = %v, want %v", tc.in, got.Average(), tc.average)
			}
		})
	}
}

func TestExprRollStaysInRange(t *testing.T) {
	expr := MustParseExpr("2d6+3")
	r := New(1)
	for i := 0; i < 2000; i++ {
		got := expr.Roll(r)
		if got < 5 || got > 15 {
			t.Fatalf("roll %d of 2d6+3 is outside [5,15]", got)
		}
	}
}

func TestExprRollCritDoublesDiceOnly(t *testing.T) {
	expr := MustParseExpr("1d6+10")
	r := New(2)
	for i := 0; i < 2000; i++ {
		got := expr.RollCrit(r)
		// Two d6 plus the untouched flat 10.
		if got < 12 || got > 22 {
			t.Fatalf("crit roll %d of 1d6+10 is outside [12,22]", got)
		}
	}
}

func TestRollNeverNegative(t *testing.T) {
	expr := MustParseExpr("1d4-10")
	r := New(3)
	for i := 0; i < 500; i++ {
		if got := expr.Roll(r); got != 0 {
			t.Fatalf("roll of 1d4-10 = %d, want 0", got)
		}
	}
}

func TestSameSeedSameRolls(t *testing.T) {
	expr := MustParseExpr("3d8+2")
	a, b := New(1234), New(1234)
	for i := 0; i < 100; i++ {
		got, want := expr.Roll(a), expr.Roll(b)
		if got != want {
			t.Fatalf("roll %d: seeded rollers diverged, %d != %d", i, got, want)
		}
	}
	// Two different seeds must not produce the same sequence.
	left, right := New(1), New(2)
	same := true
	for i := 0; i < 20; i++ {
		if left.D20() != right.D20() {
			same = false
			break
		}
	}
	if same {
		t.Error("seeds 1 and 2 produced 20 identical d20 rolls")
	}
}

func TestHasFlatAndWithFlat(t *testing.T) {
	if MustParseExpr("2d6").HasFlat() {
		t.Error("2d6 should carry no flat modifier")
	}
	if !MustParseExpr("2d6+5").HasFlat() {
		t.Error("2d6+5 should carry a flat modifier")
	}

	got := MustParseExpr("1d8").WithFlat(4)
	if got.String() != "1d8+4" {
		t.Errorf("WithFlat(4) = %q, want %q", got.String(), "1d8+4")
	}
	if got := MustParseExpr("1d8").WithFlat(-2).String(); got != "1d8-2" {
		t.Errorf("WithFlat(-2) = %q, want %q", got, "1d8-2")
	}
	if got := MustParseExpr("1d8").WithFlat(0).String(); got != "1d8" {
		t.Errorf("WithFlat(0) = %q, want it unchanged", got)
	}
}

func TestDAndChanceBounds(t *testing.T) {
	r := New(7)
	for i := 0; i < 1000; i++ {
		if got := r.D20(); got < 1 || got > 20 {
			t.Fatalf("D20() = %d, outside [1,20]", got)
		}
	}
	if got := r.D(0); got != 0 {
		t.Errorf("D(0) = %d, want 0", got)
	}
	if !r.Chance(100) {
		t.Error("Chance(100) must always succeed")
	}
	if r.Chance(0) {
		t.Error("Chance(0) must never succeed")
	}
}

func TestPick(t *testing.T) {
	r := New(11)
	if got := r.Pick(nil); got != "" {
		t.Errorf("Pick(nil) = %q, want an empty string", got)
	}

	options := []string{"goblin", "skeleton", "ghoul"}
	seen := map[string]bool{}
	for i := 0; i < 300; i++ {
		got := r.Pick(options)
		if !strings.Contains(strings.Join(options, " "), got) {
			t.Fatalf("Pick returned %q, which is not in the list", got)
		}
		seen[got] = true
	}
	if len(seen) != len(options) {
		t.Errorf("Pick only ever returned %d of %d options", len(seen), len(options))
	}
}

func TestMustParseExprPanicsOnGarbage(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("MustParseExpr should panic on a bad expression")
		}
	}()
	MustParseExpr("not dice")
}
