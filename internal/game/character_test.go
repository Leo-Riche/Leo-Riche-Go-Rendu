package game

import (
	"strings"
	"testing"
)

func TestNewCharacterDefaults(t *testing.T) {
	c := NewCharacter("  Leo  ")
	switch {
	case c.Name != "Leo":
		t.Errorf("name = %q, want it trimmed", c.Name)
	case c.Level != 1:
		t.Errorf("level = %d, want 1", c.Level)
	case c.HP != c.MaxHP || c.MaxHP != startHP:
		t.Errorf("HP = %d/%d, want %d/%d", c.HP, c.MaxHP, startHP, startHP)
	case c.AttackBonus != startAttackBonus:
		t.Errorf("attack bonus = %d, want %d", c.AttackBonus, startAttackBonus)
	case c.Weapon.DamageDice != "1d6":
		t.Errorf("starting weapon = %+v", c.Weapon)
	case !c.Alive():
		t.Error("a new character must be alive")
	}

	if got := NewCharacter("   ").Name; got != "Wanderer" {
		t.Errorf("an empty name became %q, want the default", got)
	}
}

func TestXPForLevel(t *testing.T) {
	tests := map[int]int{1: 0, 2: 100, 3: 400, 4: 900, 5: 1600, 6: 2500}
	for level, want := range tests {
		if got := XPForLevel(level); got != want {
			t.Errorf("XPForLevel(%d) = %d, want %d", level, got, want)
		}
	}
	if got := XPForLevel(0); got != 0 {
		t.Errorf("XPForLevel(0) = %d, want 0", got)
	}
}

func TestGainXPLevelsUpAndHeals(t *testing.T) {
	c := NewCharacter("Leo")
	c.Hurt(20)

	lines := c.GainXP(99)
	if c.Level != 1 {
		t.Fatalf("99 XP should not level a character, level = %d", c.Level)
	}
	if c.XPToNextLevel() != 1 {
		t.Errorf("XP to next level = %d, want 1", c.XPToNextLevel())
	}
	if len(lines) != 1 {
		t.Errorf("gaining XP without levelling printed %v", lines)
	}

	lines = c.GainXP(1)
	if c.Level != 2 {
		t.Fatalf("level = %d, want 2 at 100 XP", c.Level)
	}
	if c.MaxHP != startHP+hpPerLevel {
		t.Errorf("max HP = %d, want %d", c.MaxHP, startHP+hpPerLevel)
	}
	if c.HP != c.MaxHP {
		t.Errorf("levelling up should heal: HP = %d/%d", c.HP, c.MaxHP)
	}
	if c.AttackBonus != startAttackBonus+1 {
		t.Errorf("attack bonus = %d, want %d", c.AttackBonus, startAttackBonus+1)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "level 2") {
		t.Errorf("level-up lines = %v", lines)
	}
}

func TestGainXPCanCrossSeveralLevelsAtOnce(t *testing.T) {
	c := NewCharacter("Leo")
	c.GainXP(1000)

	if c.Level != 4 {
		t.Errorf("level = %d, want 4 at 1000 XP", c.Level)
	}
	if c.MaxHP != startHP+3*hpPerLevel {
		t.Errorf("max HP = %d, want %d", c.MaxHP, startHP+3*hpPerLevel)
	}
	c.GainXP(-50)
	if c.XP != 1000 {
		t.Errorf("negative XP changed the total to %d", c.XP)
	}
}

func TestHurtHealAndFullHeal(t *testing.T) {
	c := NewCharacter("Leo")

	c.Hurt(5)
	if c.HP != startHP-5 {
		t.Errorf("HP = %d, want %d", c.HP, startHP-5)
	}
	if got := c.Heal(2); got != 2 || c.HP != startHP-3 {
		t.Errorf("Heal(2) restored %d, HP = %d", got, c.HP)
	}
	if got := c.Heal(1000); got != 3 || c.HP != c.MaxHP {
		t.Errorf("healing past the maximum restored %d, HP = %d/%d", got, c.HP, c.MaxHP)
	}
	if got := c.Heal(5); got != 0 {
		t.Errorf("healing at full HP restored %d, want 0", got)
	}

	c.Hurt(1000)
	if c.HP != 0 || c.Alive() {
		t.Errorf("HP = %d, alive = %v, want 0 and false", c.HP, c.Alive())
	}
	if got := c.FullHeal(); got != c.MaxHP {
		t.Errorf("FullHeal restored %d, want %d", got, c.MaxHP)
	}
}

func TestArmorClassAndDamageScaleWithLevel(t *testing.T) {
	c := NewCharacter("Leo")
	ac1, dmg1 := c.ArmorClass(), c.DamageExpr().Average()

	c.Level = 9
	if c.ArmorClass() <= ac1 {
		t.Errorf("armour class at level 9 = %d, want more than %d", c.ArmorClass(), ac1)
	}
	if c.DamageExpr().Average() <= dmg1 {
		t.Errorf("damage at level 9 = %v, want more than %v", c.DamageExpr().Average(), dmg1)
	}
	if got := c.DamageExpr().String(); got != "1d6+11" {
		t.Errorf("damage expression = %q, want 1d6+11", got)
	}
}

func TestWeaponStringAndExpr(t *testing.T) {
	w := Weapon{Name: "Battleaxe", DamageDice: "1d8", DamageType: "slashing"}
	if got := w.String(); got != "Battleaxe (1d8 slashing)" {
		t.Errorf("String() = %q", got)
	}
	if got := w.Expr().String(); got != "1d8" {
		t.Errorf("Expr() = %q", got)
	}

	// A weapon with broken data still has to roll something.
	broken := Weapon{Name: "Rusted thing", DamageDice: "???"}
	if got := broken.Expr().String(); got != "1d4" {
		t.Errorf("a broken weapon rolls %q, want the 1d4 fallback", got)
	}
	if got := (Weapon{}).String(); !strings.Contains(got, "fists") {
		t.Errorf("an empty weapon = %q, want bare fists", got)
	}
}

func TestAddWeaponIgnoresDuplicates(t *testing.T) {
	c := NewCharacter("Leo")
	axe := Weapon{Name: "Battleaxe", DamageDice: "1d8"}

	if !c.AddWeapon(axe) {
		t.Fatal("the first pick-up should be kept")
	}
	if c.AddWeapon(axe) {
		t.Error("the same weapon must not be picked up twice")
	}
	if c.AddWeapon(Weapon{Name: "notched shortsword", DamageDice: "1d6"}) {
		t.Error("a weapon already in hand must not be picked up")
	}
	if c.AddWeapon(Weapon{}) {
		t.Error("a nameless weapon must not be picked up")
	}
	if len(c.Pack) != 1 {
		t.Errorf("pack = %+v, want one weapon", c.Pack)
	}
}

func TestEquipByPartialName(t *testing.T) {
	c := NewCharacter("Leo")
	c.AddWeapon(Weapon{Name: "Greatsword", DamageDice: "2d6"})

	if _, err := c.Equip("great"); err != nil {
		t.Fatalf("Equip returned %v", err)
	}
	if c.Weapon.Name != "Greatsword" {
		t.Errorf("weapon = %q", c.Weapon.Name)
	}
	if len(c.Pack) != 1 || c.Pack[0].Name != "Notched shortsword" {
		t.Errorf("pack = %+v, want the old weapon back in it", c.Pack)
	}
	if _, err := c.Equip(""); err == nil {
		t.Error("equipping nothing must fail")
	}
	if _, err := c.Equip("halberd"); err == nil {
		t.Error("equipping a weapon you do not carry must fail")
	}
}

func TestSpellLookupAndSpending(t *testing.T) {
	c := NewCharacter("Leo")
	c.Spells = []Spell{
		{Slug: "fire-bolt", Name: "Fire Bolt", Dice: "1d10+2"},
		{Slug: "cure-wounds", Name: "Cure Wounds", Dice: "1d8", Heals: true},
	}

	if _, ok := c.Spell("fire bolt"); !ok {
		t.Error("lookup by name should work")
	}
	if _, ok := c.Spell("cure-wounds"); !ok {
		t.Error("lookup by slug should work")
	}
	if _, ok := c.Spell("cure"); !ok {
		t.Error("lookup by prefix should work")
	}
	if _, ok := c.Spell("counterspell"); ok {
		t.Error("an unknown spell must not be found")
	}
	if _, ok := c.Spell(""); ok {
		t.Error("an empty name must not match")
	}

	if c.SpellUsed("fire-bolt") {
		t.Error("a fresh spell is not spent")
	}
	c.MarkSpellUsed("fire-bolt")
	if !c.SpellUsed("fire-bolt") {
		t.Error("the spell was not marked as spent")
	}
	c.ResetSpells()
	if c.SpellUsed("fire-bolt") {
		t.Error("ResetSpells did not make the spell ready again")
	}
}

func TestSpellExprFallback(t *testing.T) {
	if got := (Spell{Dice: "3d8"}).Expr().String(); got != "3d8" {
		t.Errorf("Expr() = %q", got)
	}
	if got := (Spell{Dice: "magic"}).Expr().String(); got != "2d6" {
		t.Errorf("a broken spell rolls %q, want the 2d6 fallback", got)
	}
}

func TestSheetShowsTheEssentials(t *testing.T) {
	c := NewCharacter("Leo")
	c.AddWeapon(Weapon{Name: "Greatsword", DamageDice: "2d6", DamageType: "slashing"})
	c.Spells = []Spell{{Slug: "fire-bolt", Name: "Fire Bolt", Dice: "1d10"}}
	c.MarkSpellUsed("fire-bolt")

	sheet := c.Sheet()
	for _, want := range []string{"Leo", "level 1", "30/30", "d20+4", "Notched shortsword", "Greatsword", "Fire Bolt", "spent", "gp"} {
		if !strings.Contains(sheet, want) {
			t.Errorf("the character sheet does not mention %q:\n%s", want, sheet)
		}
	}
}
