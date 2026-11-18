package game

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/leoriche/grimhold/internal/open5e"
)

// decodeMagicItem turns the potion fixture into the struct the client returns.
func decodeMagicItem(t *testing.T, raw string) *open5e.MagicItem {
	t.Helper()
	var item open5e.MagicItem
	if err := json.Unmarshal([]byte(raw), &item); err != nil {
		t.Fatalf("fixture does not decode: %v", err)
	}
	return &item
}

func TestPotionsFromTheHealingTable(t *testing.T) {
	potions, err := PotionsFrom(decodeMagicItem(t, testPotionJSON))
	if err != nil {
		t.Fatalf("PotionsFrom returned %v", err)
	}
	if len(potions) != 4 {
		t.Fatalf("got %d potions, want the 4 rows of the table", len(potions))
	}

	want := []struct {
		name  string
		heal  string
		price int
	}{
		{"Potion of Healing", "2d4+2", 50},
		{"Potion of Greater Healing", "4d4+4", 150},
		{"Potion of Superior Healing", "8d4+8", 400},
		{"Potion of Supreme Healing", "10d4+20", 900},
	}
	for i, tc := range want {
		got := potions[i]
		switch {
		case got.Name != tc.name:
			t.Errorf("potion %d name = %q, want %q", i, got.Name, tc.name)
		case got.Heal != tc.heal:
			t.Errorf("%s heals %q, want %q", got.Name, got.Heal, tc.heal)
		case got.Price != tc.price:
			t.Errorf("%s costs %d, want %d", got.Name, got.Price, tc.price)
		case got.Kind != KindPotion:
			t.Errorf("%s is a %q, want a potion", got.Name, got.Kind)
		case got.Count != 1:
			t.Errorf("%s comes in %d, want 1", got.Name, got.Count)
		}
		if got.HealExpr().String() != tc.heal {
			t.Errorf("%s rolls %q, want %q", got.Name, got.HealExpr(), tc.heal)
		}
	}
}

func TestPotionsFromRejectsAnEntryWithoutATable(t *testing.T) {
	const raw = `{"slug":"bag-of-beans","name":"Bag of Beans","desc":"Inside this bag are beans."}`
	if _, err := PotionsFrom(decodeMagicItem(t, raw)); err == nil {
		t.Error("an item with no healing table must be refused")
	}
	if _, err := PotionsFrom(nil); err == nil {
		t.Error("PotionsFrom(nil) must return an error")
	}
}

func TestHealExprFallsBack(t *testing.T) {
	if got := (Item{Heal: "glug"}).HealExpr().String(); got != "2d4+2" {
		t.Errorf("a broken potion rolls %q, want the 2d4+2 fallback", got)
	}
}

func TestBookPriceDoublesWithLevel(t *testing.T) {
	tests := map[int]int{0: 150, 1: 300, 2: 600, 3: 1200, 4: 2400, -1: 150}
	for level, want := range tests {
		if got := BookPrice(level); got != want {
			t.Errorf("BookPrice(%d) = %d, want %d", level, got, want)
		}
	}
	if BookPrice(99) != BookPrice(6) {
		t.Error("the price should stop doubling past level 6")
	}
}

func TestBookForWrapsASpell(t *testing.T) {
	book := BookFor(Spell{Slug: "burning-hands", Name: "Burning Hands", Level: 1, Dice: "3d6"})

	switch {
	case book.Kind != KindBook:
		t.Errorf("kind = %q, want book", book.Kind)
	case book.Name != "Grimoire: Burning Hands":
		t.Errorf("name = %q", book.Name)
	case book.Price != 300:
		t.Errorf("price = %d, want 300 for a level 1 spell", book.Price)
	case book.Spell == nil || book.Spell.Slug != "burning-hands":
		t.Errorf("the book carries %+v", book.Spell)
	}
	// The label must not repeat the spell name.
	if got := book.Label(); got != "Grimoire: Burning Hands (level 1, damage 3d6)" {
		t.Errorf("label = %q", got)
	}
}

func TestPriceFromCost(t *testing.T) {
	tests := map[string]int{
		"50 gp":      50,
		"10 gp":      10,
		"1 gp":       1,
		"5 sp":       1, // half a gold piece rounds up to the minimum
		"1 sp":       1,
		"5 cp":       1,
		"2 pp":       20,
		"1,500 gp":   1500,
		"":           1,
		"free":       1,
		"lots of gp": 1,
		"10 shells":  1,
	}
	for cost, want := range tests {
		if got := PriceFromCost(cost); got != want {
			t.Errorf("PriceFromCost(%q) = %d, want %d", cost, got, want)
		}
	}
}

func TestSellPriceIsHalf(t *testing.T) {
	tests := map[int]int{50: 25, 300: 150, 3: 1, 1: 1, 0: 1, -10: 1}
	for price, want := range tests {
		if got := SellPrice(price); got != want {
			t.Errorf("SellPrice(%d) = %d, want %d", price, got, want)
		}
	}
}

func TestAddItemStacks(t *testing.T) {
	c := NewCharacter("Leo")
	potion := Item{Slug: "potion-of-healing", Name: "Potion of Healing", Kind: KindPotion, Price: 50, Count: 1}

	c.AddItem(potion)
	c.AddItem(potion)
	if len(c.Items) != 1 {
		t.Fatalf("items = %+v, want one stack", c.Items)
	}
	if c.Items[0].Count != 2 {
		t.Errorf("stack = %d, want 2", c.Items[0].Count)
	}

	c.AddItem(Item{Slug: "grimoire-fire-bolt", Name: "Grimoire: Fire Bolt", Kind: KindBook, Price: 150})
	if len(c.Items) != 2 {
		t.Errorf("a different item should start its own stack: %+v", c.Items)
	}
	if c.Items[1].Count != 1 {
		t.Errorf("an item added with no count should come in 1, got %d", c.Items[1].Count)
	}

	c.AddItem(Item{Name: "nameless"})
	if len(c.Items) != 2 {
		t.Error("an item with no slug must be ignored")
	}
}

func TestTakeItemRemovesOneCopy(t *testing.T) {
	c := NewCharacter("Leo")
	potion := Item{Slug: "potion-of-healing", Name: "Potion of Healing", Kind: KindPotion, Count: 1}
	c.AddItem(potion)
	c.AddItem(potion)

	got, ok := c.TakeItem("potion-of-healing")
	if !ok {
		t.Fatal("taking a carried item failed")
	}
	if got.Count != 1 {
		t.Errorf("the taken item has count %d, want 1", got.Count)
	}
	if c.Items[0].Count != 1 {
		t.Errorf("the stack is now %d, want 1", c.Items[0].Count)
	}

	if _, ok := c.TakeItem("potion-of-healing"); !ok {
		t.Fatal("taking the last copy failed")
	}
	if len(c.Items) != 0 {
		t.Errorf("an empty stack should be removed: %+v", c.Items)
	}
	if _, ok := c.TakeItem("potion-of-healing"); ok {
		t.Error("taking from an empty bag must fail")
	}
}

func TestFindItemPrefersExactThenPrefixThenCheapest(t *testing.T) {
	c := NewCharacter("Leo")
	c.AddItem(Item{Slug: "potion-of-greater-healing", Name: "Potion of Greater Healing", Kind: KindPotion, Price: 150})
	c.AddItem(Item{Slug: "potion-of-healing", Name: "Potion of Healing", Kind: KindPotion, Price: 50})
	c.AddItem(Item{Slug: "grimoire-cure-wounds", Name: "Grimoire: Cure Wounds", Kind: KindBook, Price: 300})

	tests := map[string]string{
		"Potion of Greater Healing": "Potion of Greater Healing", // exact name
		"potion-of-healing":         "Potion of Healing",         // slug
		"grimoire":                  "Grimoire: Cure Wounds",     // prefix
		"healing":                   "Potion of Healing",         // cheapest of two matches
		"greater":                   "Potion of Greater Healing",
	}
	for needle, want := range tests {
		got, ok := c.FindItem(needle)
		if !ok {
			t.Errorf("FindItem(%q) found nothing", needle)
			continue
		}
		if got.Name != want {
			t.Errorf("FindItem(%q) = %q, want %q", needle, got.Name, want)
		}
	}

	if _, ok := c.FindItem("ice knife"); ok {
		t.Error("FindItem must not invent an item")
	}
	if _, ok := c.FindItem("  "); ok {
		t.Error("an empty needle must not match")
	}
}

func TestFindItemKindFiltersByKind(t *testing.T) {
	c := NewCharacter("Leo")
	c.AddItem(Item{Slug: "potion-of-healing", Name: "Potion of Healing", Kind: KindPotion, Price: 50})
	c.AddItem(Item{Slug: "grimoire-healing-word", Name: "Grimoire: Healing Word", Kind: KindBook, Price: 300})

	got, ok := c.FindItemKind("healing", KindBook)
	if !ok || got.Kind != KindBook {
		t.Errorf("looking for a book called healing gave %+v", got)
	}
	got, ok = c.FindItemKind("healing", KindPotion)
	if !ok || got.Kind != KindPotion {
		t.Errorf("looking for a potion called healing gave %+v", got)
	}
	if _, ok := c.FindItemKind("healing", KindWeapon); ok {
		t.Error("there is no weapon in the bag")
	}
}

func TestKnowsAndLearnSpell(t *testing.T) {
	c := NewCharacter("Leo")
	spell := Spell{Slug: "fire-bolt", Name: "Fire Bolt", Dice: "1d10"}

	if c.KnowsSpell("fire-bolt") {
		t.Error("a new character knows no spells")
	}
	if !c.LearnSpell(spell) {
		t.Fatal("learning a new spell should succeed")
	}
	if !c.KnowsSpell("fire-bolt") {
		t.Error("the spell was not learned")
	}
	if c.LearnSpell(spell) {
		t.Error("learning the same spell twice must be refused")
	}
	if c.LearnSpell(Spell{Name: "No Slug"}) {
		t.Error("a spell with no slug must be refused")
	}
	if len(c.Spells) != 1 {
		t.Errorf("spells = %+v, want exactly one", c.Spells)
	}
}

func TestBagLines(t *testing.T) {
	c := NewCharacter("Leo")
	if got := strings.Join(c.BagLines(), "\n"); !strings.Contains(got, "bag is empty") {
		t.Errorf("an empty bag should say so, got %q", got)
	}

	c.AddWeapon(Weapon{Name: "Greatsword", DamageDice: "2d6", Cost: "50 gp"})
	c.AddItem(Item{Slug: "potion-of-healing", Name: "Potion of Healing", Kind: KindPotion, Price: 50, Count: 2, Heal: "2d4+2"})

	got := strings.Join(c.BagLines(), "\n")
	for _, want := range []string{"Notched shortsword", "Greatsword", "sells for 25 gp", "Potion of Healing", "x2", "10 gp in your purse"} {
		if !strings.Contains(got, want) {
			t.Errorf("the bag listing is missing %q:\n%s", want, got)
		}
	}
}

func TestWeaponPriceFallsBackToDamage(t *testing.T) {
	if got := weaponPrice(Weapon{Name: "Battleaxe", DamageDice: "1d8", Cost: "10 gp"}); got != 10 {
		t.Errorf("a priced weapon is worth %d, want 10", got)
	}
	// No cost in the API entry: worth about twice its average damage.
	if got := weaponPrice(Weapon{Name: "Found blade", DamageDice: "2d6"}); got != 14 {
		t.Errorf("an unpriced weapon is worth %d, want 14", got)
	}
	if got := weaponPrice(Weapon{Name: "Twig"}); got < 1 {
		t.Errorf("every weapon is worth at least 1 gp, got %d", got)
	}
}
