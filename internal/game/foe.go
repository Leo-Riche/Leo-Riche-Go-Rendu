package game

import (
	"fmt"
	"strings"

	"github.com/leoriche/grimhold/internal/ansi"
	"github.com/leoriche/grimhold/internal/dice"
	"github.com/leoriche/grimhold/internal/open5e"
)

// Foe is an Open5e monster rolled up for a fight. The dice expressions are
// kept as strings so that a pending encounter survives a save and a load; the
// parsed forms are rebuilt by prepare.
type Foe struct {
	Slug        string  `json:"slug"`
	Name        string  `json:"name"`
	HP          int     `json:"hp"`
	MaxHP       int     `json:"max_hp"`
	AC          int     `json:"ac"`
	AttackName  string  `json:"attack_name"`
	AttackBonus int     `json:"attack_bonus"`
	DamageDice  string  `json:"damage_dice"`
	RiderDice   string  `json:"rider_dice,omitempty"`
	CR          float64 `json:"cr"`
	CRLabel     string  `json:"cr_label"`
	XP          int     `json:"xp"`
	Boss        bool    `json:"boss,omitempty"`
	Improvised  bool    `json:"improvised,omitempty"`

	damage dice.Expr
	rider  dice.Expr
}

// NewFoe turns a monster stat block into a combatant.
//
// Open5e writes a monster's whole damage output into one expression, riders
// included: the Mummy Lord's rotting fist is "3d6+6d6" because the second
// group is rot that only lands when the target fails a saving throw. Grimhold
// has no saving throws, so the first dice group plus any flat modifier is the
// damage on a hit, and the remaining groups become rider damage that only
// lands on a critical. Without that split, every high-CR monster would be
// unkillable.
func NewFoe(m *open5e.Monster, boss bool) (*Foe, error) {
	if m == nil {
		return nil, fmt.Errorf("new foe: no monster")
	}

	f := &Foe{
		Slug:    m.Slug,
		Name:    m.Name,
		HP:      m.HitPoints,
		MaxHP:   m.HitPoints,
		AC:      m.ArmorClass,
		CR:      m.CR,
		CRLabel: m.CRString(),
		XP:      XPForCR(m.CR),
		Boss:    boss,
	}
	if f.Name == "" {
		f.Name = "something nameless"
	}
	if f.MaxHP <= 0 {
		f.HP, f.MaxHP = 4, 4
	}
	if f.AC <= 0 {
		f.AC = 10
	}

	if action, ok := m.BestAttack(); ok {
		f.AttackName = action.Name
		f.AttackBonus = *action.AttackBonus
		hit, rider := splitDamage(action.DamageDice)
		if action.DamageBonus != nil && !hit.HasFlat() {
			hit = hit.WithFlat(*action.DamageBonus)
		}
		f.DamageDice = hit.String()
		if !rider.IsZero() {
			f.RiderDice = rider.String()
		}
	} else {
		// A stat block with no rollable attack (some swarms, some casters)
		// gets one improvised from its challenge rating.
		f.Improvised = true
		f.AttackName = "savage assault"
		f.AttackBonus = 2 + int(m.CR/2)
		f.DamageDice = fmt.Sprintf("1d6+%d", 1+int(m.CR/2))
	}
	if err := f.prepare(); err != nil {
		return nil, fmt.Errorf("new foe %q: %w", f.Slug, err)
	}
	return f, nil
}

// splitDamage separates an expression into the damage a hit deals and the
// rider that only lands on a critical. See NewFoe for why.
func splitDamage(raw string) (hit, rider dice.Expr) {
	parsed, err := dice.ParseExpr(raw)
	if err != nil || parsed.IsZero() {
		return dice.MustParseExpr("1d6"), dice.Expr{}
	}

	groups := strings.Split(strings.ReplaceAll(strings.ToLower(strings.TrimSpace(raw)), " ", ""), "+")
	var dicePart []string
	var flat []string
	for _, g := range groups {
		if g == "" {
			continue
		}
		if strings.Contains(g, "d") {
			dicePart = append(dicePart, g)
		} else {
			flat = append(flat, g)
		}
	}
	if len(dicePart) == 0 {
		return parsed, dice.Expr{}
	}

	hitParts := append([]string{dicePart[0]}, flat...)
	hit, err = dice.ParseExpr(strings.Join(hitParts, "+"))
	if err != nil {
		return parsed, dice.Expr{}
	}
	if len(dicePart) > 1 {
		if r, err := dice.ParseExpr(strings.Join(dicePart[1:], "+")); err == nil {
			rider = r
		}
	}
	return hit, rider
}

// prepare rebuilds the parsed dice expressions. It is called by NewFoe and
// again after a save file is loaded.
func (f *Foe) prepare() error {
	d, err := dice.ParseExpr(f.DamageDice)
	if err != nil {
		return fmt.Errorf("damage %q: %w", f.DamageDice, err)
	}
	if d.IsZero() {
		d = dice.MustParseExpr("1d6")
	}
	f.damage = d

	f.rider = dice.Expr{}
	if f.RiderDice != "" {
		r, err := dice.ParseExpr(f.RiderDice)
		if err != nil {
			return fmt.Errorf("rider damage %q: %w", f.RiderDice, err)
		}
		f.rider = r
	}
	return nil
}

// Alive reports whether the foe is still standing.
func (f *Foe) Alive() bool { return f != nil && f.HP > 0 }

// Hurt removes hit points, floored at zero.
func (f *Foe) Hurt(n int) {
	if n < 0 {
		n = 0
	}
	f.HP -= n
	if f.HP < 0 {
		f.HP = 0
	}
}

// Describe is the one-line summary printed when an encounter starts and when
// the player looks at the fight again.
func (f *Foe) Describe() string {
	title := ansi.Red(f.Name)
	if f.Boss {
		title = ansi.Magenta(f.Name)
	}
	line := fmt.Sprintf("%s  CR %s  AC %d  %s", title, f.CRLabel, f.AC, ansi.HP(f.HP, f.MaxHP, 14))
	line += fmt.Sprintf("\n  attacks with %s at d20+%d for %s", f.AttackName, f.AttackBonus, f.damage)
	if !f.rider.IsZero() {
		line += fmt.Sprintf(" (+%s on a critical)", f.rider)
	}
	if f.Improvised {
		line += "\n  " + ansi.Gray("(no attack in its stat block; improvised from its challenge rating)")
	}
	return line
}

// xpTable is the 5e experience award per challenge rating, used for XP gains.
var xpTable = []struct {
	cr float64
	xp int
}{
	{0, 10}, {0.125, 25}, {0.25, 50}, {0.5, 100},
	{1, 200}, {2, 450}, {3, 700}, {4, 1100}, {5, 1800},
	{6, 2300}, {7, 2900}, {8, 3900}, {9, 5000}, {10, 5900},
	{11, 7200}, {12, 8400}, {13, 10000}, {14, 11500}, {15, 13000},
	{16, 15000}, {17, 18000}, {18, 20000}, {19, 22000}, {20, 25000},
	{21, 33000}, {22, 41000}, {23, 50000}, {24, 62000}, {25, 75000},
	{26, 90000}, {27, 105000}, {28, 120000}, {29, 135000}, {30, 155000},
}

// XPForCR returns the experience a foe of the given challenge rating is worth.
// Ratings between two table rows round down to the lower row.
func XPForCR(cr float64) int {
	if cr <= 0 {
		return xpTable[0].xp
	}
	xp := xpTable[0].xp
	for _, row := range xpTable {
		if cr+1e-9 < row.cr {
			break
		}
		xp = row.xp
	}
	return xp
}
