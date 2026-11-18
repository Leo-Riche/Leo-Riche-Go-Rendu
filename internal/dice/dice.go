// Package dice provides the randomness Grimhold needs: polyhedral dice and the
// "2d6+3" style damage expressions returned by the Open5e API.
//
// Every roll in the game goes through a single *Roller so that a test can
// seed it and replay an identical fight.
package dice

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"
)

// Roller is a seedable source of dice rolls. It is not safe for concurrent
// use: the game rolls dice from the REPL goroutine only.
type Roller struct {
	rnd *rand.Rand
}

// New returns a Roller with a fixed seed. Two rollers built from the same seed
// produce the same sequence of rolls, which is what the combat tests rely on.
func New(seed int64) *Roller {
	return &Roller{rnd: rand.New(rand.NewSource(seed))}
}

// NewRandom returns a Roller seeded from the clock.
func NewRandom() *Roller { return New(time.Now().UnixNano()) }

// D rolls one die of the given number of sides. Sides below 1 roll 0.
func (r *Roller) D(sides int) int {
	if sides < 1 {
		return 0
	}
	return r.rnd.Intn(sides) + 1
}

// D20 rolls the attack die.
func (r *Roller) D20() int { return r.D(20) }

// Intn returns a value in [0,n). It is used for percentage checks such as the
// chance that an ambush actually happens.
func (r *Roller) Intn(n int) int {
	if n < 1 {
		return 0
	}
	return r.rnd.Intn(n)
}

// Chance reports whether a percent-in-100 check succeeds.
func (r *Roller) Chance(percent int) bool {
	if percent <= 0 {
		return false
	}
	if percent >= 100 {
		return true
	}
	return r.Intn(100) < percent
}

// Pick returns a random element of s, or "" when s is empty.
func (r *Roller) Pick(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[r.Intn(len(s))]
}

// term is one component of an expression: either N dice of M sides, or a flat
// modifier. sign is +1 or -1.
type term struct {
	count int
	sides int
	flat  int
	sign  int
}

func (t term) isDice() bool { return t.count > 0 && t.sides > 0 }

// Expr is a parsed damage expression such as "1d6", "2d6+3" or "3d6+6d6".
// The zero Expr rolls 0 and reports IsZero.
type Expr struct {
	terms []term
	src   string
}

// ParseExpr parses a dice expression. An empty string yields the zero Expr
// without an error, because the API omits damage dice for many actions.
func ParseExpr(s string) (Expr, error) {
	raw := strings.TrimSpace(s)
	norm := strings.ToLower(strings.ReplaceAll(raw, " ", ""))
	if norm == "" {
		return Expr{src: raw}, nil
	}

	e := Expr{src: raw}
	sign := 1
	start := 0
	for i := 0; i <= len(norm); i++ {
		if i != len(norm) && norm[i] != '+' && norm[i] != '-' {
			continue
		}
		if part := norm[start:i]; part != "" {
			t, err := parseTerm(part, sign)
			if err != nil {
				return Expr{}, fmt.Errorf("dice: bad expression %q: %w", raw, err)
			}
			e.terms = append(e.terms, t)
		}
		if i < len(norm) {
			if norm[i] == '-' {
				sign = -1
			} else {
				sign = 1
			}
		}
		start = i + 1
	}
	if len(e.terms) == 0 {
		return Expr{}, fmt.Errorf("dice: bad expression %q: no terms", raw)
	}
	return e, nil
}

func parseTerm(part string, sign int) (term, error) {
	if !strings.Contains(part, "d") {
		n, err := strconv.Atoi(part)
		if err != nil {
			return term{}, fmt.Errorf("%q is not a number", part)
		}
		return term{flat: n, sign: sign}, nil
	}
	left, right, _ := strings.Cut(part, "d")
	count := 1
	if left != "" {
		n, err := strconv.Atoi(left)
		if err != nil {
			return term{}, fmt.Errorf("%q is not a dice count", left)
		}
		count = n
	}
	sides, err := strconv.Atoi(right)
	if err != nil {
		return term{}, fmt.Errorf("%q is not a number of sides", right)
	}
	if count < 1 || sides < 1 {
		return term{}, fmt.Errorf("%q must be positive", part)
	}
	if count > 100 || sides > 1000 {
		return term{}, fmt.Errorf("%q is out of range", part)
	}
	return term{count: count, sides: sides, sign: sign}, nil
}

// MustParseExpr is ParseExpr for expressions written in the source, such as
// starting weapons. It panics on a bad expression.
func MustParseExpr(s string) Expr {
	e, err := ParseExpr(s)
	if err != nil {
		panic(err)
	}
	return e
}

// IsZero reports whether the expression has no terms at all.
func (e Expr) IsZero() bool { return len(e.terms) == 0 }

// HasFlat reports whether the expression already carries a flat modifier.
// Open5e sometimes writes the modifier into damage_dice ("2d6+5") and
// sometimes into a separate damage_bonus field; this tells the two apart so
// the bonus is not counted twice.
func (e Expr) HasFlat() bool {
	for _, t := range e.terms {
		if !t.isDice() {
			return true
		}
	}
	return false
}

// WithFlat returns a copy of e with n added as a flat modifier.
func (e Expr) WithFlat(n int) Expr {
	if n == 0 {
		return e
	}
	out := Expr{terms: make([]term, 0, len(e.terms)+1), src: e.src}
	out.terms = append(out.terms, e.terms...)
	sign := 1
	if n < 0 {
		sign, n = -1, -n
	}
	out.terms = append(out.terms, term{flat: n, sign: sign})
	out.src = out.String()
	return out
}

// Roll rolls the expression. The result is never negative.
func (e Expr) Roll(r *Roller) int { return e.roll(r, 1) }

// RollCrit rolls the expression with every dice term doubled, which is how a
// critical hit is scored in Grimhold.
func (e Expr) RollCrit(r *Roller) int { return e.roll(r, 2) }

func (e Expr) roll(r *Roller, diceMult int) int {
	total := 0
	for _, t := range e.terms {
		if !t.isDice() {
			total += t.sign * t.flat
			continue
		}
		sum := 0
		for i := 0; i < t.count*diceMult; i++ {
			sum += r.D(t.sides)
		}
		total += t.sign * sum
	}
	if total < 0 {
		return 0
	}
	return total
}

// Average is the expected value of the expression, used to decide which of a
// monster's actions is its best attack.
func (e Expr) Average() float64 {
	total := 0.0
	for _, t := range e.terms {
		if t.isDice() {
			total += float64(t.sign) * float64(t.count) * (float64(t.sides) + 1) / 2
			continue
		}
		total += float64(t.sign * t.flat)
	}
	if total < 0 {
		return 0
	}
	return total
}

// String renders the expression in canonical form ("2d6+3").
func (e Expr) String() string {
	if e.IsZero() {
		return "0"
	}
	var b strings.Builder
	for i, t := range e.terms {
		switch {
		case i > 0 && t.sign < 0:
			b.WriteByte('-')
		case i > 0:
			b.WriteByte('+')
		case t.sign < 0:
			b.WriteByte('-')
		}
		if t.isDice() {
			fmt.Fprintf(&b, "%dd%d", t.count, t.sides)
		} else {
			fmt.Fprintf(&b, "%d", t.flat)
		}
	}
	return b.String()
}
