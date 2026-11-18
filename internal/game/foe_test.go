package game

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/leoriche/grimhold/internal/open5e"
)

// decodeMonster is a helper that turns one of the JSON fixtures into a
// monster, the same way the client does.
func decodeMonster(t *testing.T, raw string) *open5e.Monster {
	t.Helper()
	var m open5e.Monster
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("fixture does not decode: %v", err)
	}
	return &m
}

func TestNewFoeFromAStatBlock(t *testing.T) {
	foe, err := NewFoe(decodeMonster(t, testGoblinJSON), false)
	if err != nil {
		t.Fatalf("NewFoe returned %v", err)
	}

	switch {
	case foe.Name != "Goblin":
		t.Errorf("name = %q", foe.Name)
	case foe.HP != 7 || foe.MaxHP != 7:
		t.Errorf("HP = %d/%d, want 7/7", foe.HP, foe.MaxHP)
	case foe.AC != 15:
		t.Errorf("AC = %d, want 15", foe.AC)
	case foe.AttackName != "Scimitar":
		t.Errorf("attack = %q, want Scimitar", foe.AttackName)
	case foe.AttackBonus != 4:
		t.Errorf("attack bonus = %d, want 4", foe.AttackBonus)
	case foe.XP != 50:
		t.Errorf("XP = %d, want 50 for CR 1/4", foe.XP)
	case foe.Boss:
		t.Error("an ordinary foe must not be marked as a boss")
	}

	// damage_dice is "1d6" and damage_bonus is 2, so the bonus must be folded
	// in exactly once.
	if foe.DamageDice != "1d6+2" {
		t.Errorf("damage = %q, want 1d6+2", foe.DamageDice)
	}
	if foe.RiderDice != "" {
		t.Errorf("rider = %q, want none", foe.RiderDice)
	}
}

func TestNewFoeSplitsRiderDamage(t *testing.T) {
	foe, err := NewFoe(decodeMonster(t, testMummyLordJSON), true)
	if err != nil {
		t.Fatalf("NewFoe returned %v", err)
	}

	// "3d6+6d6" with damage_bonus 4: the first dice group plus the flat bonus
	// is the damage on a hit, the second group is a critical-only rider.
	if foe.DamageDice != "3d6+4" {
		t.Errorf("damage = %q, want 3d6+4", foe.DamageDice)
	}
	if foe.RiderDice != "6d6" {
		t.Errorf("rider = %q, want 6d6", foe.RiderDice)
	}
	if !foe.Boss {
		t.Error("the boss flag was not set")
	}
	if foe.XP != 13000 {
		t.Errorf("XP = %d, want 13000 for CR 15", foe.XP)
	}

	if desc := foe.Describe(); !strings.Contains(desc, "on a critical") {
		t.Errorf("Describe should mention the rider:\n%s", desc)
	}
}

func TestNewFoeDoesNotDoubleCountAnEmbeddedModifier(t *testing.T) {
	// damage_dice already carries "+5" and there is no damage_bonus field.
	const raw = `{"slug":"a","name":"A","armor_class":14,"hit_points":20,"cr":7,
		"challenge_rating":"7",
		"actions":[{"name":"Bite","attack_bonus":8,"damage_dice":"2d6+5"}]}`

	foe, err := NewFoe(decodeMonster(t, raw), false)
	if err != nil {
		t.Fatalf("NewFoe returned %v", err)
	}
	if foe.DamageDice != "2d6+5" {
		t.Errorf("damage = %q, want 2d6+5", foe.DamageDice)
	}
}

func TestNewFoeImprovisesWhenThereIsNoAttack(t *testing.T) {
	foe, err := NewFoe(decodeMonster(t, testSwarmJSON), false)
	if err != nil {
		t.Fatalf("NewFoe returned %v", err)
	}

	if !foe.Improvised {
		t.Error("a stat block with no attack should be flagged as improvised")
	}
	if foe.AttackBonus < 1 {
		t.Errorf("improvised attack bonus = %d, want something usable", foe.AttackBonus)
	}
	if foe.DamageDice == "" {
		t.Error("an improvised attack still needs damage dice")
	}
	if desc := foe.Describe(); !strings.Contains(desc, "improvised") {
		t.Errorf("Describe should own up to the improvisation:\n%s", desc)
	}
}

func TestNewFoeRepairsABrokenStatBlock(t *testing.T) {
	const raw = `{"slug":"husk","name":"","armor_class":0,"hit_points":0,"cr":0,
		"actions":[{"name":"Flail","attack_bonus":1,"damage_dice":""}]}`

	foe, err := NewFoe(decodeMonster(t, raw), false)
	if err != nil {
		t.Fatalf("NewFoe returned %v", err)
	}
	switch {
	case foe.Name == "":
		t.Error("a nameless monster should still get a name")
	case foe.MaxHP <= 0:
		t.Errorf("MaxHP = %d, want a usable default", foe.MaxHP)
	case foe.AC <= 0:
		t.Errorf("AC = %d, want a usable default", foe.AC)
	}
	if _, err := NewFoe(nil, false); err == nil {
		t.Error("NewFoe(nil) must return an error")
	}
}

func TestFoeHurtAndAlive(t *testing.T) {
	foe := &Foe{Name: "Ogre", HP: 10, MaxHP: 10, DamageDice: "1d6"}
	if err := foe.prepare(); err != nil {
		t.Fatalf("prepare returned %v", err)
	}

	if !foe.Alive() {
		t.Error("a foe with hit points is alive")
	}
	foe.Hurt(4)
	if foe.HP != 6 {
		t.Errorf("HP = %d, want 6", foe.HP)
	}
	foe.Hurt(-5)
	if foe.HP != 6 {
		t.Errorf("negative damage changed HP to %d", foe.HP)
	}
	foe.Hurt(100)
	if foe.HP != 0 {
		t.Errorf("HP = %d, want 0 rather than a negative number", foe.HP)
	}
	if foe.Alive() {
		t.Error("a foe at 0 HP is not alive")
	}

	var nilFoe *Foe
	if nilFoe.Alive() {
		t.Error("a nil foe must not be alive")
	}
}

func TestFoePrepareRejectsGarbageDice(t *testing.T) {
	foe := &Foe{Name: "Thing", HP: 5, MaxHP: 5, DamageDice: "lots"}
	if err := foe.prepare(); err == nil {
		t.Error("prepare should reject an unparseable damage expression")
	}

	foe = &Foe{Name: "Thing", HP: 5, MaxHP: 5, DamageDice: "1d6", RiderDice: "nonsense"}
	if err := foe.prepare(); err == nil {
		t.Error("prepare should reject an unparseable rider expression")
	}
}

func TestSplitDamage(t *testing.T) {
	tests := []struct {
		in        string
		wantHit   string
		wantRider string
	}{
		{in: "1d6", wantHit: "1d6", wantRider: "0"},
		{in: "2d6+5", wantHit: "2d6+5", wantRider: "0"},
		{in: "3d6+6d6", wantHit: "3d6", wantRider: "6d6"},
		{in: "1d8+4+2d6", wantHit: "1d8+4", wantRider: "2d6"},
		{in: "2d10+3d10+4d10", wantHit: "2d10", wantRider: "3d10+4d10"},
		{in: "7", wantHit: "7", wantRider: "0"},
		{in: "", wantHit: "1d6", wantRider: "0"},
		{in: "garbage", wantHit: "1d6", wantRider: "0"},
	}

	for _, tc := range tests {
		hit, rider := splitDamage(tc.in)
		if hit.String() != tc.wantHit {
			t.Errorf("splitDamage(%q) hit = %q, want %q", tc.in, hit.String(), tc.wantHit)
		}
		if rider.String() != tc.wantRider {
			t.Errorf("splitDamage(%q) rider = %q, want %q", tc.in, rider.String(), tc.wantRider)
		}
	}
}

func TestXPForCR(t *testing.T) {
	tests := map[float64]int{
		0:     10,
		0.125: 25,
		0.25:  50,
		0.5:   100,
		1:     200,
		2:     450,
		5:     1800,
		15:    13000,
		30:    155000,
		// Ratings between rows round down to the lower row.
		0.3: 50,
		7.5: 2900,
		// Out of range values are clamped.
		-1: 10,
		99: 155000,
	}
	for cr, want := range tests {
		if got := XPForCR(cr); got != want {
			t.Errorf("XPForCR(%v) = %d, want %d", cr, got, want)
		}
	}
}
