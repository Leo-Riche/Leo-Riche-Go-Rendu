// Package open5e is a small read-only client for the Open5e API
// (https://api.open5e.com), the source of every monster, weapon and spell in
// Grimhold. Responses are decoded into typed structs and cached in memory.
package open5e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Page is one page of a paginated Open5e list endpoint. Next and Previous are
// absolute URLs supplied by the API, and are empty on the first and last page.
type Page[T any] struct {
	Count    int    `json:"count"`
	Next     string `json:"next"`
	Previous string `json:"previous"`
	Results  []T    `json:"results"`
}

// Action is an attack, legendary action or special ability of a monster.
// AttackBonus and DamageBonus are pointers because the API omits them for
// actions that are not attacks, and zero is a meaningful value.
type Action struct {
	Name        string `json:"name"`
	Desc        string `json:"desc"`
	AttackBonus *int   `json:"attack_bonus,omitempty"`
	DamageDice  string `json:"damage_dice,omitempty"`
	DamageBonus *int   `json:"damage_bonus,omitempty"`
}

// IsAttack reports whether the action can be rolled as an attack.
func (a Action) IsAttack() bool { return a.AttackBonus != nil && a.DamageDice != "" }

// ActionList tolerates the two shapes the API uses for action arrays: a real
// array, or an empty string for entries with no actions at all.
type ActionList []Action

// UnmarshalJSON implements json.Unmarshaler.
func (l *ActionList) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		*l = nil
		return nil
	}
	if data[0] == '"' {
		// The API sends an empty string instead of an empty array for some
		// monsters; that is not an error, it just means no actions.
		*l = nil
		return nil
	}
	var out []Action
	if err := json.Unmarshal(data, &out); err != nil {
		return fmt.Errorf("decode actions: %w", err)
	}
	*l = out
	return nil
}

// Monster is an Open5e monster stat block. Only the fields Grimhold uses are
// modelled; the rest of the payload is ignored.
type Monster struct {
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Desc      string `json:"desc"`
	Size      string `json:"size"`
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	Alignment string `json:"alignment"`

	ArmorClass int    `json:"armor_class"`
	ArmorDesc  string `json:"armor_desc"`
	HitPoints  int    `json:"hit_points"`
	HitDice    string `json:"hit_dice"`

	// Speed values are mostly numbers but can be booleans (hover: true), so
	// the map is deliberately untyped.
	Speed map[string]any `json:"speed"`

	Strength     int `json:"strength"`
	Dexterity    int `json:"dexterity"`
	Constitution int `json:"constitution"`
	Intelligence int `json:"intelligence"`
	Wisdom       int `json:"wisdom"`
	Charisma     int `json:"charisma"`

	DamageVulnerabilities string `json:"damage_vulnerabilities"`
	DamageResistances     string `json:"damage_resistances"`
	DamageImmunities      string `json:"damage_immunities"`
	ConditionImmunities   string `json:"condition_immunities"`
	Senses                string `json:"senses"`
	Languages             string `json:"languages"`

	ChallengeRating string  `json:"challenge_rating"`
	CR              float64 `json:"cr"`

	Actions          ActionList `json:"actions"`
	BonusActions     ActionList `json:"bonus_actions"`
	Reactions        ActionList `json:"reactions"`
	LegendaryActions ActionList `json:"legendary_actions"`
	SpecialAbilities ActionList `json:"special_abilities"`
}

// Weapon is an entry of the /v1/weapons/ endpoint.
type Weapon struct {
	Name       string   `json:"name"`
	Slug       string   `json:"slug"`
	Category   string   `json:"category"`
	Cost       string   `json:"cost"`
	DamageDice string   `json:"damage_dice"`
	DamageType string   `json:"damage_type"`
	Weight     string   `json:"weight"`
	Properties []string `json:"properties"`
	Document   string   `json:"document__slug"`
}

// Melee reports whether the weapon is a melee weapon, the only kind Grimhold
// lets the player swing.
func (w Weapon) Melee() bool { return strings.Contains(strings.ToLower(w.Category), "melee") }

// Spell is an entry of the /v1/spells/ endpoint.
type Spell struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Desc        string `json:"desc"`
	HigherLevel string `json:"higher_level"`
	Range       string `json:"range"`
	Components  string `json:"components"`
	Duration    string `json:"duration"`
	CastingTime string `json:"casting_time"`
	Level       string `json:"level"`
	LevelInt    int    `json:"level_int"`
	School      string `json:"school"`
	Class       string `json:"dnd_class"`
}

// BestAttack returns the monster's most damaging rollable attack. ok is false
// when the stat block carries no attack at all (some spellcasters and swarms),
// in which case the caller invents one from the challenge rating.
func (m *Monster) BestAttack() (Action, bool) {
	var best Action
	bestScore := -1.0
	for _, a := range m.Actions {
		if !a.IsAttack() {
			continue
		}
		if score := averageDamage(a); score > bestScore {
			best, bestScore = a, score
		}
	}
	return best, bestScore >= 0
}

// averageDamage scores an action without pulling in the dice package: a rough
// sum of NdM groups plus any flat modifier is enough to rank attacks.
func averageDamage(a Action) float64 {
	total := 0.0
	for _, part := range strings.Split(strings.ReplaceAll(strings.ToLower(a.DamageDice), " ", ""), "+") {
		if part == "" {
			continue
		}
		count, sides, found := strings.Cut(part, "d")
		if !found {
			total += atof(part)
			continue
		}
		n := atof(count)
		if count == "" {
			n = 1
		}
		total += n * (atof(sides) + 1) / 2
	}
	if a.DamageBonus != nil && !strings.ContainsAny(a.DamageDice, "+-") {
		total += float64(*a.DamageBonus)
	}
	return total
}

// atof reads the leading digits of s as a number, ignoring any trailing junk.
func atof(s string) float64 {
	var n float64
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return n
		}
		n = n*10 + float64(s[i]-'0')
	}
	return n
}

// CRString renders the challenge rating, preferring the string the API sends
// ("1/4") over the float.
func (m *Monster) CRString() string {
	if m.ChallengeRating != "" {
		return m.ChallengeRating
	}
	return fmt.Sprintf("%g", m.CR)
}

// StatBlock renders the monster the way the inspect command prints it.
func (m *Monster) StatBlock() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", m.Name)

	kind := strings.TrimSpace(m.Size + " " + m.Type)
	if m.Subtype != "" {
		kind += " (" + m.Subtype + ")"
	}
	if m.Alignment != "" {
		kind += ", " + m.Alignment
	}
	fmt.Fprintf(&b, "%s\n", kind)

	ac := fmt.Sprintf("%d", m.ArmorClass)
	if m.ArmorDesc != "" {
		ac += " (" + m.ArmorDesc + ")"
	}
	fmt.Fprintf(&b, "Armor Class  %s\n", ac)

	hp := fmt.Sprintf("%d", m.HitPoints)
	if m.HitDice != "" {
		hp += " (" + m.HitDice + ")"
	}
	fmt.Fprintf(&b, "Hit Points   %s\n", hp)

	if speed := m.speedString(); speed != "" {
		fmt.Fprintf(&b, "Speed        %s\n", speed)
	}
	fmt.Fprintf(&b, "STR %d  DEX %d  CON %d  INT %d  WIS %d  CHA %d\n",
		m.Strength, m.Dexterity, m.Constitution, m.Intelligence, m.Wisdom, m.Charisma)
	fmt.Fprintf(&b, "Challenge    %s\n", m.CRString())

	writeIf(&b, "Vulnerable   ", m.DamageVulnerabilities)
	writeIf(&b, "Resistant    ", m.DamageResistances)
	writeIf(&b, "Immune       ", m.DamageImmunities)
	writeIf(&b, "Cond. immune ", m.ConditionImmunities)
	writeIf(&b, "Senses       ", m.Senses)
	writeIf(&b, "Languages    ", m.Languages)

	writeActions(&b, "Actions", m.Actions)
	writeActions(&b, "Legendary actions", m.LegendaryActions)
	writeActions(&b, "Special abilities", m.SpecialAbilities)
	return strings.TrimRight(b.String(), "\n")
}

func writeIf(b *strings.Builder, label, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	fmt.Fprintf(b, "%s%s\n", label, value)
}

func writeActions(b *strings.Builder, title string, actions ActionList) {
	if len(actions) == 0 {
		return
	}
	fmt.Fprintf(b, "\n%s:\n", title)
	for _, a := range actions {
		desc := strings.TrimSpace(strings.ReplaceAll(a.Desc, "\n", " "))
		if len(desc) > 220 {
			desc = desc[:217] + "..."
		}
		fmt.Fprintf(b, "  %s. %s\n", a.Name, desc)
	}
}

func (m *Monster) speedString() string {
	if len(m.Speed) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m.Speed))
	for k := range m.Speed {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		switch v := m.Speed[k].(type) {
		case float64:
			parts = append(parts, fmt.Sprintf("%s %g ft.", k, v))
		case bool:
			if v {
				parts = append(parts, k)
			}
		case string:
			parts = append(parts, fmt.Sprintf("%s %s", k, v))
		}
	}
	return strings.Join(parts, ", ")
}

// Line renders a monster as a single row for the bestiary listing.
func (m *Monster) Line() string {
	return fmt.Sprintf("%-30s CR %-5s AC %-3d HP %-4d %s",
		m.Name, m.CRString(), m.ArmorClass, m.HitPoints, strings.ToLower(m.Type))
}
