// Package game holds everything that is not input parsing or networking: the
// character, the foes, the dice-based combat, the bestiary of kills and the
// save file.
package game

import (
	"fmt"
	"sort"
	"strings"

	"github.com/leoriche/grimhold/internal/ansi"
	"github.com/leoriche/grimhold/internal/dice"
)

// Starting values for a fresh character.
const (
	startHP          = 30
	startAttackBonus = 4
	startGold        = 10
	hpPerLevel       = 10
	baseArmorClass   = 13
)

// Weapon is what the character swings. Damage is a dice expression such as
// "1d8"; the character's own damage bonus is added on top.
type Weapon struct {
	Name       string `json:"name"`
	DamageDice string `json:"damage_dice"`
	DamageType string `json:"damage_type"`
	Cost       string `json:"cost,omitempty"`
}

// StartingWeapon is what everyone picks up off the gallows at the crossroads.
func StartingWeapon() Weapon {
	return Weapon{Name: "Notched shortsword", DamageDice: "1d6", DamageType: "slashing"}
}

// String renders the weapon as "Battleaxe (1d8 slashing)".
func (w Weapon) String() string {
	if w.Name == "" {
		return "bare fists (1d2 bludgeoning)"
	}
	dmg := w.DamageDice
	if dmg == "" {
		dmg = "1d4"
	}
	if w.DamageType != "" {
		dmg += " " + w.DamageType
	}
	return fmt.Sprintf("%s (%s)", w.Name, dmg)
}

// Expr is the weapon's damage dice, defaulting to 1d4 for a broken entry.
func (w Weapon) Expr() dice.Expr {
	e, err := dice.ParseExpr(w.DamageDice)
	if err != nil || e.IsZero() {
		return dice.MustParseExpr("1d4")
	}
	return e
}

// Spell is one of the two spells chosen at character creation. Dice is the
// first dice group found in the spell description, which is how a spell copied
// straight out of the API gets a number attached to it.
type Spell struct {
	Slug  string `json:"slug"`
	Name  string `json:"name"`
	Level int    `json:"level"`
	Dice  string `json:"dice"`
	Heals bool   `json:"heals"`
	Desc  string `json:"desc,omitempty"`
}

// Expr is the spell's dice, defaulting to 2d6 when the description had none.
func (s Spell) Expr() dice.Expr {
	e, err := dice.ParseExpr(s.Dice)
	if err != nil || e.IsZero() {
		return dice.MustParseExpr("2d6")
	}
	return e
}

// Character is the player.
type Character struct {
	Name        string          `json:"name"`
	Level       int             `json:"level"`
	XP          int             `json:"xp"`
	HP          int             `json:"hp"`
	MaxHP       int             `json:"max_hp"`
	AttackBonus int             `json:"attack_bonus"`
	Gold        int             `json:"gold"`
	Weapon      Weapon          `json:"weapon"`
	Pack        []Weapon        `json:"pack,omitempty"`
	Spells      []Spell         `json:"spells,omitempty"`
	SpellsUsed  map[string]bool `json:"spells_used,omitempty"`
}

// NewCharacter rolls up a level 1 character.
func NewCharacter(name string) *Character {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Wanderer"
	}
	return &Character{
		Name:        name,
		Level:       1,
		HP:          startHP,
		MaxHP:       startHP,
		AttackBonus: startAttackBonus,
		Gold:        startGold,
		Weapon:      StartingWeapon(),
		SpellsUsed:  map[string]bool{},
	}
}

// Alive reports whether the character still has hit points.
func (c *Character) Alive() bool { return c.HP > 0 }

// ArmorClass is what a foe has to beat to land a hit. It creeps up with level
// to stand for better reflexes and better scavenged armour.
func (c *Character) ArmorClass() int {
	return baseArmorClass + (c.Level-1)/2
}

// DamageBonus is added to every weapon damage roll.
func (c *Character) DamageBonus() int { return 2 + c.Level }

// DamageExpr is the full damage roll: weapon dice plus damage bonus.
func (c *Character) DamageExpr() dice.Expr {
	return c.Weapon.Expr().WithFlat(c.DamageBonus())
}

// Hurt removes hit points, floored at zero.
func (c *Character) Hurt(n int) {
	if n < 0 {
		n = 0
	}
	c.HP -= n
	if c.HP < 0 {
		c.HP = 0
	}
}

// Heal restores hit points, capped at MaxHP, and returns how many it restored.
func (c *Character) Heal(n int) int {
	if n < 0 || c.HP >= c.MaxHP {
		return 0
	}
	before := c.HP
	c.HP += n
	if c.HP > c.MaxHP {
		c.HP = c.MaxHP
	}
	return c.HP - before
}

// FullHeal sets hit points back to maximum and returns how many it restored.
func (c *Character) FullHeal() int { return c.Heal(c.MaxHP) }

// XPForLevel is the total experience needed to reach the given level:
// (level-1)^2 * 100. So 100 XP for level 2, 400 for level 3, 900 for level 4.
func XPForLevel(level int) int {
	if level <= 1 {
		return 0
	}
	n := level - 1
	return n * n * 100
}

// XPToNextLevel is how much more experience the character needs.
func (c *Character) XPToNextLevel() int {
	need := XPForLevel(c.Level+1) - c.XP
	if need < 0 {
		return 0
	}
	return need
}

// GainXP awards experience and applies every level-up it earns. The returned
// lines are printed by the caller.
func (c *Character) GainXP(amount int) []string {
	if amount < 0 {
		amount = 0
	}
	c.XP += amount
	lines := []string{fmt.Sprintf("You gain %s experience (%d total).", ansi.Cyan(fmt.Sprintf("%d", amount)), c.XP)}

	for c.XP >= XPForLevel(c.Level+1) {
		c.Level++
		c.MaxHP += hpPerLevel
		c.AttackBonus++
		c.HP = c.MaxHP
		lines = append(lines, ansi.Bold(fmt.Sprintf("You reach level %d.", c.Level)),
			fmt.Sprintf("  Max HP %d, attack bonus +%d, armour class %d. Your wounds close over.",
				c.MaxHP, c.AttackBonus, c.ArmorClass()))
	}
	return lines
}

// AddWeapon puts a weapon in the pack, ignoring duplicates.
func (c *Character) AddWeapon(w Weapon) bool {
	if w.Name == "" {
		return false
	}
	if strings.EqualFold(w.Name, c.Weapon.Name) {
		return false
	}
	for _, held := range c.Pack {
		if strings.EqualFold(held.Name, w.Name) {
			return false
		}
	}
	c.Pack = append(c.Pack, w)
	return true
}

// Equip swaps a weapon out of the pack into the character's hands. The
// previously held weapon goes back into the pack.
func (c *Character) Equip(name string) (Weapon, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Weapon{}, fmt.Errorf("equip what?")
	}
	for i, w := range c.Pack {
		if !strings.EqualFold(w.Name, name) && !strings.Contains(strings.ToLower(w.Name), strings.ToLower(name)) {
			continue
		}
		old := c.Weapon
		c.Weapon = w
		c.Pack = append(c.Pack[:i:i], c.Pack[i+1:]...)
		if old.Name != "" {
			c.Pack = append(c.Pack, old)
		}
		return w, nil
	}
	return Weapon{}, fmt.Errorf("you are not carrying anything called %q", name)
}

// ResetSpells makes every spell castable again, which happens on a rest.
func (c *Character) ResetSpells() {
	c.SpellsUsed = map[string]bool{}
}

// Spell finds a known spell by name or slug.
func (c *Character) Spell(name string) (Spell, bool) {
	needle := strings.ToLower(strings.TrimSpace(name))
	if needle == "" {
		return Spell{}, false
	}
	for _, s := range c.Spells {
		if strings.ToLower(s.Name) == needle || s.Slug == needle ||
			strings.Contains(strings.ToLower(s.Name), needle) {
			return s, true
		}
	}
	return Spell{}, false
}

// SpellUsed reports whether a spell has already been cast since the last rest.
func (c *Character) SpellUsed(slug string) bool {
	return c.SpellsUsed[slug]
}

// MarkSpellUsed spends a spell until the next rest.
func (c *Character) MarkSpellUsed(slug string) {
	if c.SpellsUsed == nil {
		c.SpellsUsed = map[string]bool{}
	}
	c.SpellsUsed[slug] = true
}

// Sheet renders the character sheet printed by the character command.
func (c *Character) Sheet() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s, level %d\n", ansi.Bold(c.Name), c.Level)
	fmt.Fprintf(&b, "  HP          %s\n", ansi.HP(c.HP, c.MaxHP, 20))
	fmt.Fprintf(&b, "  XP          %d (%d to level %d)\n", c.XP, c.XPToNextLevel(), c.Level+1)
	fmt.Fprintf(&b, "  Attack      d20+%d vs armour class\n", c.AttackBonus)
	fmt.Fprintf(&b, "  Damage      %s with %s\n", c.DamageExpr(), c.Weapon)
	fmt.Fprintf(&b, "  Armour      %d\n", c.ArmorClass())
	fmt.Fprintf(&b, "  Gold        %d gp\n", c.Gold)

	if len(c.Pack) > 0 {
		names := make([]string, 0, len(c.Pack))
		for _, w := range c.Pack {
			names = append(names, w.String())
		}
		sort.Strings(names)
		fmt.Fprintf(&b, "  Pack        %s\n", strings.Join(names, ", "))
	}
	if len(c.Spells) > 0 {
		parts := make([]string, 0, len(c.Spells))
		for _, s := range c.Spells {
			state := ansi.Green("ready")
			if c.SpellUsed(s.Slug) {
				state = ansi.Gray("spent")
			}
			parts = append(parts, fmt.Sprintf("%s [%s]", s.Name, state))
		}
		fmt.Fprintf(&b, "  Spells      %s\n", strings.Join(parts, ", "))
	}
	return strings.TrimRight(b.String(), "\n")
}
