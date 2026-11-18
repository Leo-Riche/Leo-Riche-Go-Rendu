package game

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/leoriche/grimhold/internal/ansi"
	"github.com/leoriche/grimhold/internal/dice"
	"github.com/leoriche/grimhold/internal/open5e"
)

// ItemKind says what an item does when it is used.
type ItemKind string

// The kinds of thing a merchant deals in.
const (
	KindPotion ItemKind = "potion"
	KindBook   ItemKind = "book"
	KindWeapon ItemKind = "weapon"
)

// HealingPotionSlug is the Open5e magic item that carries the whole table of
// healing potions in its description.
const HealingPotionSlug = "potion-of-healing"

// Item is something carryable that is not held in your hands: a potion to
// drink or a grimoire to read. Weapons live in the character's pack instead,
// because only one of them can be wielded at a time.
type Item struct {
	Slug  string   `json:"slug"`
	Name  string   `json:"name"`
	Kind  ItemKind `json:"kind"`
	Price int      `json:"price"`
	Count int      `json:"count"`

	// Heal is the dice a potion restores, such as "2d4+2".
	Heal string `json:"heal,omitempty"`
	// Spell is what a grimoire teaches, for good, when it is read.
	Spell *Spell `json:"spell,omitempty"`
	// Note is the one-line flavour shown in the shop and the inventory.
	Note string `json:"note,omitempty"`
}

// HealExpr is the potion's dice, with a sane fallback for a broken entry.
func (i Item) HealExpr() dice.Expr {
	e, err := dice.ParseExpr(i.Heal)
	if err != nil || e.IsZero() {
		return dice.MustParseExpr("2d4+2")
	}
	return e
}

// Label renders the item for a list, without its price.
func (i Item) Label() string {
	switch i.Kind {
	case KindPotion:
		return fmt.Sprintf("%s (restores %s)", i.Name, i.Heal)
	case KindBook:
		if i.Spell != nil {
			return fmt.Sprintf("%s (%s)", i.Name, i.Spell.Details())
		}
		return i.Name
	default:
		return i.Name
	}
}

// SellPrice is what a merchant pays for an item: half, rounded down, and never
// less than one gold piece.
func SellPrice(price int) int {
	if price <= 1 {
		return 1
	}
	return price / 2
}

// potionRow matches one line of the Potions of Healing table that Open5e
// ships inside the Potion of Healing description:
//
//	| Greater healing  | Uncommon  | 4d4 + 4     |
var potionRow = regexp.MustCompile(`\|\s*([A-Za-z][A-Za-z ]*?)\s*\|\s*([A-Za-z][A-Za-z ]*?)\s*\|\s*([0-9]{1,2}d[0-9]{1,2}(?:\s*\+\s*[0-9]{1,3})?)\s*\|`)

// potionPrices is what each rarity costs in gold. The API knows the rarity of
// a potion but not its price, so the economy is Grimhold's own.
var potionPrices = map[string]int{
	"common":    50,
	"uncommon":  150,
	"rare":      400,
	"very rare": 900,
	"legendary": 2000,
}

// PotionsFrom turns the Open5e Potion of Healing entry into the four potions a
// merchant can stock, cheapest first. The dice and the rarities come straight
// out of the table in the item description.
func PotionsFrom(item *open5e.MagicItem) ([]Item, error) {
	if item == nil {
		return nil, fmt.Errorf("potions: no magic item")
	}

	var out []Item
	for _, row := range potionRow.FindAllStringSubmatch(item.Desc, -1) {
		grade := strings.TrimSpace(row[1])
		rarity := strings.ToLower(strings.TrimSpace(row[2]))
		heal := strings.ReplaceAll(strings.TrimSpace(row[3]), " ", "")

		// The table's header row matches the pattern too, but its third
		// column is not dice, so it never reaches here. Guard anyway.
		if _, err := dice.ParseExpr(heal); err != nil {
			continue
		}

		name := "Potion of " + titleCase(grade)
		price, ok := potionPrices[rarity]
		if !ok {
			price = 50
		}
		out = append(out, Item{
			Slug:  open5e.Slugify(name),
			Name:  name,
			Kind:  KindPotion,
			Price: price,
			Count: 1,
			Heal:  heal,
			Note:  rarity,
		})
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("potions: no healing table in the %q entry", item.Name)
	}
	return out, nil
}

// titleCase capitalises every word, so the table's "greater healing" becomes
// "Greater Healing".
func titleCase(s string) string {
	words := strings.Fields(strings.ToLower(s))
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// BookPrice is what a grimoire costs: 150 gold for a cantrip, doubling with
// every spell level.
func BookPrice(level int) int {
	if level < 0 {
		level = 0
	}
	if level > 6 {
		level = 6
	}
	price := 150
	for i := 0; i < level; i++ {
		price *= 2
	}
	return price
}

// BookFor wraps a spell as the grimoire that teaches it.
func BookFor(spell Spell) Item {
	return Item{
		Slug:  "grimoire-" + spell.Slug,
		Name:  "Grimoire: " + spell.Name,
		Kind:  KindBook,
		Price: BookPrice(spell.Level),
		Count: 1,
		Spell: &spell,
		Note:  "read it to learn the spell for good",
	}
}

// coinValues converts the coin names Open5e uses into gold pieces.
var coinValues = map[string]float64{"gp": 1, "sp": 0.1, "ep": 0.5, "cp": 0.01, "pp": 10}

// PriceFromCost reads a weapon cost such as "10 gp" or "1 sp" and returns it
// in whole gold pieces, never below one.
func PriceFromCost(cost string) int {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(cost)))
	if len(fields) < 2 {
		return 1
	}
	amount, err := strconv.ParseFloat(strings.ReplaceAll(fields[0], ",", ""), 64)
	if err != nil {
		return 1
	}
	rate, ok := coinValues[fields[1]]
	if !ok {
		return 1
	}
	gold := int(amount * rate)
	if gold < 1 {
		return 1
	}
	return gold
}

// AddItem puts an item in the bag, stacking it with one of the same slug.
func (c *Character) AddItem(item Item) {
	if item.Slug == "" {
		return
	}
	if item.Count < 1 {
		item.Count = 1
	}
	for i := range c.Items {
		if c.Items[i].Slug == item.Slug {
			c.Items[i].Count += item.Count
			return
		}
	}
	c.Items = append(c.Items, item)
}

// TakeItem removes one copy of an item from the bag and returns it.
func (c *Character) TakeItem(slug string) (Item, bool) {
	for i := range c.Items {
		if c.Items[i].Slug != slug {
			continue
		}
		item := c.Items[i]
		item.Count = 1
		if c.Items[i].Count > 1 {
			c.Items[i].Count--
		} else {
			c.Items = append(c.Items[:i:i], c.Items[i+1:]...)
		}
		return item, true
	}
	return Item{}, false
}

// FindItem looks an item up in the bag by slug, by name, or by any
// distinctive part of its name ("healing", "greater", "burning hands").
func (c *Character) FindItem(name string) (Item, bool) {
	return c.FindItemKind(name, "")
}

// FindItemKind is FindItem restricted to one kind of item, so that "read
// healing" finds the grimoire and "drink healing" finds the potion. An empty
// kind matches anything.
func (c *Character) FindItemKind(name string, kind ItemKind) (Item, bool) {
	needle := strings.ToLower(strings.TrimSpace(name))
	if needle == "" {
		return Item{}, false
	}
	slug := open5e.Slugify(needle)

	items := c.Items
	if kind != "" {
		items = items[:0:0]
		for _, item := range c.Items {
			if item.Kind == kind {
				items = append(items, item)
			}
		}
	}

	for _, item := range items {
		if item.Slug == slug || strings.ToLower(item.Name) == needle {
			return item, true
		}
	}
	for _, item := range items {
		if strings.HasPrefix(strings.ToLower(item.Name), needle) {
			return item, true
		}
	}

	// Several things can contain the same word ("healing" matches both the
	// plain and the greater potion). Take the cheapest, which is the one a
	// careful player means.
	var best Item
	found := false
	for _, item := range items {
		if !strings.Contains(strings.ToLower(item.Name), needle) {
			continue
		}
		if !found || item.Price < best.Price {
			best, found = item, true
		}
	}
	return best, found
}

// KnowsSpell reports whether the character already knows a spell.
func (c *Character) KnowsSpell(slug string) bool {
	for _, s := range c.Spells {
		if s.Slug == slug {
			return true
		}
	}
	return false
}

// LearnSpell adds a spell for good. It reports false if it was already known.
func (c *Character) LearnSpell(spell Spell) bool {
	if spell.Slug == "" || c.KnowsSpell(spell.Slug) {
		return false
	}
	c.Spells = append(c.Spells, spell)
	return true
}

// BagLines renders the bag for the inventory command.
func (c *Character) BagLines() []string {
	if len(c.Items) == 0 && len(c.Pack) == 0 {
		return []string{ansi.Gray("Your bag is empty. Loot regions and bodies, or visit a merchant.")}
	}

	lines := []string{ansi.Bold(fmt.Sprintf("Carried (%d gp in your purse):", c.Gold))}
	lines = append(lines, fmt.Sprintf("  in hand   %s", ansi.Cyan(c.Weapon.String())))
	for _, w := range c.Pack {
		lines = append(lines, fmt.Sprintf("  weapon    %-40s sells for %d gp", w.String(), SellPrice(weaponPrice(w))))
	}
	for _, item := range c.Items {
		count := ""
		if item.Count > 1 {
			count = fmt.Sprintf(" x%d", item.Count)
		}
		lines = append(lines, fmt.Sprintf("  %-9s %-40s sells for %d gp",
			item.Kind, item.Label()+count, SellPrice(item.Price)))
	}
	return lines
}

// weaponPrice is what a weapon is worth, falling back to its average damage
// when the API gave no cost.
func weaponPrice(w Weapon) int {
	if w.Cost != "" {
		return PriceFromCost(w.Cost)
	}
	if price := int(w.Expr().Average() * 2); price > 1 {
		return price
	}
	return 1
}
