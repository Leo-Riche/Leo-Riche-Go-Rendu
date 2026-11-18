package game

import (
	"context"
	"testing"

	"github.com/leoriche/grimhold/internal/open5e"
)

func TestSpellFromAPIExtractsDice(t *testing.T) {
	tests := []struct {
		name     string
		spell    open5e.Spell
		wantDice string
		wantHeal bool
	}{
		{
			name:     "cantrip gets a flat bonus",
			spell:    open5e.Spell{Slug: "fire-bolt", Name: "Fire Bolt", LevelInt: 0, Desc: "the target takes 1d10 fire damage."},
			wantDice: "1d10+2",
		},
		{
			name:     "modifier written after the dice is kept",
			spell:    open5e.Spell{Slug: "magic-missile", Name: "Magic Missile", LevelInt: 1, Desc: "Each dart deals 1d4 + 1 force damage."},
			wantDice: "1d4+1",
		},
		{
			name:     "plain dice",
			spell:    open5e.Spell{Slug: "burning-hands", Name: "Burning Hands", LevelInt: 1, Desc: "takes 3d6 fire damage."},
			wantDice: "3d6",
		},
		{
			name:     "healing is detected by name",
			spell:    open5e.Spell{Slug: "cure-wounds", Name: "Cure Wounds", LevelInt: 1, Desc: "regains 1d8 hit points."},
			wantDice: "1d8",
			wantHeal: true,
		},
		{
			name:     "no dice at all falls back",
			spell:    open5e.Spell{Slug: "shield", Name: "Shield", LevelInt: 1, Desc: "your armour class rises."},
			wantDice: "2d6",
		},
		{
			name:     "a modifier that is not a number is ignored",
			spell:    open5e.Spell{Slug: "x", Name: "X", LevelInt: 2, Desc: "deals 2d6 + your spellcasting modifier."},
			wantDice: "2d6",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SpellFromAPI(&tc.spell)
			if got.Dice != tc.wantDice {
				t.Errorf("dice = %q, want %q", got.Dice, tc.wantDice)
			}
			if got.Heals != tc.wantHeal {
				t.Errorf("heals = %v, want %v", got.Heals, tc.wantHeal)
			}
			if got.Name != tc.spell.Name || got.Slug != tc.spell.Slug {
				t.Errorf("spell = %+v, want the name and slug carried over", got)
			}
			if got.Expr().IsZero() {
				t.Error("the spell must end up with a rollable expression")
			}
		})
	}

	if got := SpellFromAPI(nil); got.Name != "" {
		t.Errorf("SpellFromAPI(nil) = %+v, want the zero spell", got)
	}
}

func TestSpellSummary(t *testing.T) {
	cantrip := Spell{Name: "Fire Bolt", Level: 0, Dice: "1d10+2"}
	if got := cantrip.Summary(); got != "Fire Bolt (cantrip, damage 1d10+2)" {
		t.Errorf("Summary() = %q", got)
	}
	healing := Spell{Name: "Cure Wounds", Level: 1, Dice: "1d8", Heals: true}
	if got := healing.Summary(); got != "Cure Wounds (level 1, healing 1d8)" {
		t.Errorf("Summary() = %q", got)
	}
}

func TestOfferSpellsWithoutAClient(t *testing.T) {
	if got := OfferSpells(context.Background(), nil); got != nil {
		t.Errorf("OfferSpells(nil client) = %+v, want nil", got)
	}
}
