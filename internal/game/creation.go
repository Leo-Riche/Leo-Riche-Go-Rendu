package game

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/leoriche/grimhold/internal/open5e"
)

// SpellsOnOffer are the spells a new character may choose from. They are
// fetched from /v1/spells/ at creation time, so their text and level come from
// the API rather than from this file.
var SpellsOnOffer = []string{
	"fire-bolt",
	"magic-missile",
	"burning-hands",
	"guiding-bolt",
	"inflict-wounds",
	"thunderwave",
	"shocking-grasp",
	"cure-wounds",
	"healing-word",
}

// SpellsPerCharacter is how many spells are picked at creation.
const SpellsPerCharacter = 2

// diceInText finds the first dice group in a spell description, which is what
// the spell rolls for in Grimhold. A flat modifier written right after the
// dice is kept too, so Magic Missile reads as 1d4+1 rather than 1d4.
var diceInText = regexp.MustCompile(`([0-9]{1,2})d([0-9]{1,3})(?:\s*\+\s*([0-9]{1,2}))?`)

// healingWords mark a spell as restoring hit points rather than removing them.
var healingWords = []string{"cure", "heal"}

// OfferSpells fetches the spell list for character creation. Spells that fail
// to load are left out; an empty result means the API could not be reached at
// all, and the caller simply starts the player without spells.
func OfferSpells(ctx context.Context, client *open5e.Client) []Spell {
	if client == nil {
		return nil
	}
	fetched := client.SpellsConcurrently(ctx, SpellsOnOffer)
	out := make([]Spell, 0, len(fetched))
	for _, s := range fetched {
		out = append(out, SpellFromAPI(s))
	}
	return out
}

// SpellFromAPI converts an API spell into the playable form: a name, a level,
// one dice expression pulled out of its description, and whether it heals.
func SpellFromAPI(s *open5e.Spell) Spell {
	if s == nil {
		return Spell{}
	}
	out := Spell{
		Slug:  s.Slug,
		Name:  s.Name,
		Level: s.LevelInt,
		Dice:  "2d6",
		Desc:  strings.TrimSpace(s.Desc),
	}

	lowerName := strings.ToLower(s.Name)
	for _, word := range healingWords {
		if strings.Contains(lowerName, word) {
			out.Heals = true
			break
		}
	}
	if m := diceInText.FindStringSubmatch(s.Desc); m != nil {
		count, _ := strconv.Atoi(m[1])
		sides, _ := strconv.Atoi(m[2])
		if count > 0 && count <= 20 && sides > 0 {
			out.Dice = fmt.Sprintf("%dd%d", count, sides)
			if flat, err := strconv.Atoi(m[3]); err == nil && flat > 0 {
				out.Dice += fmt.Sprintf("+%d", flat)
			}
		}
	}
	// Cantrips are weaker but unlimited in 5e; here every spell is once per
	// rest, so a cantrip gets a small flat bonus instead of nothing.
	if out.Level == 0 {
		out.Dice += "+2"
	}
	return out
}

// Details describes a spell without naming it: "level 1, damage 3d6".
func (s Spell) Details() string {
	kind := "damage"
	if s.Heals {
		kind = "healing"
	}
	level := "cantrip"
	if s.Level > 0 {
		level = "level " + strconv.Itoa(s.Level)
	}
	return level + ", " + kind + " " + s.Dice
}

// Summary is the one-line description shown while picking spells.
func (s Spell) Summary() string {
	return s.Name + " (" + s.Details() + ")"
}
