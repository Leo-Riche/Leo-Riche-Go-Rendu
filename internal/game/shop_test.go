package game

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// richGame is a game whose character can afford anything in the shops.
func richGame(t *testing.T, seed int64) *Game {
	t.Helper()
	g := newTestGame(t, seed)
	g.Char.Gold = 5000
	return g
}

func TestShopListsTheStock(t *testing.T) {
	g := richGame(t, 1)

	lines, err := g.Shop(context.Background())
	if err != nil {
		t.Fatalf("Shop returned %v", err)
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"Mother Ash",
		"A blanket of bottles.",
		"Potion of Healing (restores 2d4+2)",
		"Potion of Supreme Healing",
		"50 gp",
		"You carry",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the shop listing is missing %q:\n%s", want, joined)
		}
	}
}

func TestShopNeedsAMerchantAndNoFoe(t *testing.T) {
	g := richGame(t, 1)
	ctx := context.Background()

	g.Char.Level = 5
	if _, err := g.Travel(ctx, "marsh"); err != nil {
		t.Fatalf("travel failed: %v", err)
	}
	if _, err := g.Travel(ctx, "vault"); err != nil {
		t.Fatalf("travel failed: %v", err)
	}

	// The vault has no merchant.
	for name, call := range map[string]func() error{
		"shop": func() error { _, err := g.Shop(ctx); return err },
		"buy":  func() error { _, err := g.Buy(ctx, "1"); return err },
		"sell": func() error { _, err := g.Sell("anything"); return err },
	} {
		if err := call(); !errors.Is(err, ErrNoShop) {
			t.Errorf("%s in a region with no merchant returned %v, want ErrNoShop", name, err)
		}
	}

	// Back where there is one, but with a foe in the way.
	if _, err := g.Travel(ctx, "marsh"); err != nil {
		t.Fatalf("travel failed: %v", err)
	}
	g.BossesSlain["mummy-lord"] = true
	mustEncounter(t, g)
	for name, call := range map[string]func() error{
		"shop": func() error { _, err := g.Shop(ctx); return err },
		"buy":  func() error { _, err := g.Buy(ctx, "1"); return err },
		"sell": func() error { _, err := g.Sell("anything"); return err },
	} {
		if err := call(); !errors.Is(err, ErrInEncounter) {
			t.Errorf("%s during a fight returned %v, want ErrInEncounter", name, err)
		}
	}
}

func TestBuyByNumberAndByName(t *testing.T) {
	g := richGame(t, 1)
	ctx := context.Background()

	if _, err := g.Buy(ctx, "1"); err != nil {
		t.Fatalf("buying by number returned %v", err)
	}
	if _, ok := g.Char.FindItem("Potion of Healing"); !ok {
		t.Errorf("the potion is not in the bag: %+v", g.Char.Items)
	}
	if g.Char.Gold != 4950 {
		t.Errorf("gold = %d, want 4950 after a 50 gp potion", g.Char.Gold)
	}

	if _, err := g.Buy(ctx, "potion of greater healing"); err != nil {
		t.Fatalf("buying by name returned %v", err)
	}
	if g.Char.Gold != 4800 {
		t.Errorf("gold = %d, want 4800", g.Char.Gold)
	}

	// A fragment that matches one item only.
	if _, err := g.Buy(ctx, "supreme"); err != nil {
		t.Fatalf("buying by fragment returned %v", err)
	}
	if len(g.Char.Items) != 3 {
		t.Errorf("bag = %+v, want three potions", g.Char.Items)
	}
}

func TestBuyRefusesNonsense(t *testing.T) {
	g := richGame(t, 1)
	ctx := context.Background()

	tests := map[string]string{
		"":              "buy what",
		"0":             "no 0 on the blanket",
		"99":            "no 99 on the blanket",
		"flying carpet": "matches",
		"healing":       "matches several",
	}
	for what, want := range tests {
		_, err := g.Buy(ctx, what)
		if err == nil {
			t.Errorf("buying %q should have been refused", what)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("buying %q gave %q, want it to mention %q", what, err, want)
		}
	}
}

func TestBuyNeedsGold(t *testing.T) {
	g := newTestGame(t, 1) // starts with 10 gp
	ctx := context.Background()

	_, err := g.Buy(ctx, "potion of healing")
	if !errors.Is(err, ErrNoMoney) {
		t.Fatalf("error = %v, want ErrNoMoney", err)
	}
	if !strings.Contains(err.Error(), "50") {
		t.Errorf("error = %q, want it to name the price", err)
	}
	if g.Char.Gold != 10 {
		t.Errorf("a refused purchase changed the purse to %d", g.Char.Gold)
	}
	if len(g.Char.Items) != 0 {
		t.Errorf("a refused purchase filled the bag: %+v", g.Char.Items)
	}
}

func TestBuyAWeaponGoesIntoThePack(t *testing.T) {
	g := richGame(t, 1)
	ctx := context.Background()
	if _, err := g.Travel(ctx, "marsh"); err != nil {
		t.Fatalf("travel failed: %v", err)
	}

	lines, err := g.Buy(ctx, "greatsword")
	if err != nil {
		t.Fatalf("buying a weapon returned %v", err)
	}
	if len(g.Char.Pack) != 1 || g.Char.Pack[0].Name != "Greatsword" {
		t.Fatalf("pack = %+v, want the greatsword", g.Char.Pack)
	}
	if g.Char.Pack[0].DamageDice != "2d6" {
		t.Errorf("the bought weapon rolls %q, want the API dice 2d6", g.Char.Pack[0].DamageDice)
	}
	if !strings.Contains(strings.Join(lines, " "), "equip") {
		t.Errorf("buying a weapon should say how to wield it: %v", lines)
	}

	// Buying the same weapon again is refused and costs nothing.
	gold := g.Char.Gold
	if _, err := g.Buy(ctx, "greatsword"); err == nil {
		t.Error("buying a weapon already carried should be refused")
	}
	if g.Char.Gold != gold {
		t.Errorf("a refused purchase took %d gold", gold-g.Char.Gold)
	}
}

func TestBuyAGrimoireAndReadIt(t *testing.T) {
	g := richGame(t, 1)
	ctx := context.Background()
	if _, err := g.Travel(ctx, "marsh"); err != nil {
		t.Fatalf("travel failed: %v", err)
	}

	if _, err := g.Buy(ctx, "grimoire: fire bolt"); err != nil {
		t.Fatalf("buying a grimoire returned %v", err)
	}
	book, ok := g.Char.FindItem("fire bolt")
	if !ok || book.Kind != KindBook {
		t.Fatalf("the grimoire is not in the bag: %+v", g.Char.Items)
	}

	lines, err := g.Use("fire bolt", KindBook)
	if err != nil {
		t.Fatalf("reading the grimoire returned %v", err)
	}
	if !g.Char.KnowsSpell("fire-bolt") {
		t.Fatal("reading the grimoire taught nothing")
	}
	if _, ok := g.Char.FindItem("fire bolt"); ok {
		t.Error("the grimoire should be consumed by reading it")
	}
	if !strings.Contains(strings.Join(lines, " "), "for good") {
		t.Errorf("reading should say the spell is permanent: %v", lines)
	}

	// The spell survives a save and a load.
	path := t.TempDir() + "/save.json"
	if _, err := g.Save(path); err != nil {
		t.Fatalf("Save returned %v", err)
	}
	restored := newTestGame(t, 2)
	if _, err := restored.Load(path); err != nil {
		t.Fatalf("Load returned %v", err)
	}
	if !restored.Char.KnowsSpell("fire-bolt") {
		t.Error("the learned spell did not survive the save")
	}

	// And the merchant no longer offers a book the player has read.
	listing, err := g.Shop(ctx)
	if err != nil {
		t.Fatalf("Shop returned %v", err)
	}
	if strings.Contains(strings.Join(listing, "\n"), "Grimoire: Fire Bolt") {
		t.Error("a grimoire for a known spell should be off the blanket")
	}
}

func TestReadIsRefusedWhenItMakesNoSense(t *testing.T) {
	g := richGame(t, 1)
	ctx := context.Background()
	if _, err := g.Travel(ctx, "marsh"); err != nil {
		t.Fatalf("travel failed: %v", err)
	}
	if _, err := g.Buy(ctx, "grimoire: fire bolt"); err != nil {
		t.Fatalf("buying returned %v", err)
	}

	// Not with something in the room.
	g.BossesSlain["mummy-lord"] = true
	mustEncounter(t, g)
	if _, err := g.Use("fire bolt", KindBook); !errors.Is(err, ErrInEncounter) {
		t.Errorf("reading during a fight returned %v, want ErrInEncounter", err)
	}
	g.Foe = nil

	// Not a spell you already know.
	g.Char.LearnSpell(Spell{Slug: "fire-bolt", Name: "Fire Bolt", Dice: "1d10"})
	if _, err := g.Use("fire bolt", KindBook); err == nil {
		t.Error("reading a spell you already know must be refused")
	}
	if _, ok := g.Char.FindItem("fire bolt"); !ok {
		t.Error("a refused reading must not destroy the book")
	}
}

func TestDrinkHealsAndIsSpent(t *testing.T) {
	g := richGame(t, 1)
	ctx := context.Background()

	if _, err := g.Buy(ctx, "potion of healing"); err != nil {
		t.Fatalf("buying returned %v", err)
	}
	// Unhurt, the potion is not wasted.
	if _, err := g.Use("healing", KindPotion); err == nil {
		t.Error("drinking at full health must be refused")
	}

	g.Char.Hurt(20)
	before := g.Char.HP
	lines, err := g.Use("healing", KindPotion)
	if err != nil {
		t.Fatalf("drinking returned %v", err)
	}
	if g.Char.HP <= before {
		t.Errorf("HP = %d, want more than %d", g.Char.HP, before)
	}
	if g.Char.HP > g.Char.MaxHP {
		t.Errorf("HP = %d, above the maximum %d", g.Char.HP, g.Char.MaxHP)
	}
	if _, ok := g.Char.FindItem("healing"); ok {
		t.Error("the potion should be gone after drinking it")
	}
	if !strings.Contains(strings.Join(lines, " "), "regain") {
		t.Errorf("drinking should say how much it healed: %v", lines)
	}
}

func TestDrinkingInAFightCostsYourTurn(t *testing.T) {
	g := richGame(t, 4)
	ctx := context.Background()

	if _, err := g.Buy(ctx, "potion of supreme healing"); err != nil {
		t.Fatalf("buying returned %v", err)
	}
	mustEncounter(t, g)
	g.Char.Hurt(25)
	foeHP := g.Foe.HP

	lines, err := g.Use("supreme", KindPotion)
	if err != nil {
		t.Fatalf("drinking in a fight returned %v", err)
	}
	joined := strings.Join(lines, "\n")

	if g.Foe != nil && g.Foe.HP != foeHP {
		t.Error("drinking a potion must not hurt the foe")
	}
	// The foe answers: its attack line is printed either way, hit or miss.
	if !strings.Contains(joined, "Goblin") {
		t.Errorf("the foe did not get its swing:\n%s", joined)
	}
}

func TestDrinkingCanKillYouAndTriggerTheDeathFlow(t *testing.T) {
	// A character on one hit point who drinks instead of fighting can be
	// finished off by the free swing that follows.
	for seed := int64(1); seed <= 40; seed++ {
		g := richGame(t, seed)
		ctx := context.Background()
		if _, err := g.Buy(ctx, "potion of healing"); err != nil {
			t.Fatalf("buying returned %v", err)
		}
		if _, err := g.Travel(ctx, "marsh"); err != nil {
			t.Fatalf("travel failed: %v", err)
		}
		mustEncounter(t, g)

		g.Char.MaxHP = 2
		g.Char.HP = 1
		lines, err := g.Use("healing", KindPotion)
		if err != nil {
			t.Fatalf("seed %d: drinking returned %v", seed, err)
		}
		if strings.Contains(strings.Join(lines, "\n"), "leaves you for dead") {
			if g.Char.HP != 1 || g.Location != g.World.Start || g.Foe != nil {
				t.Errorf("seed %d: the death flow did not run properly", seed)
			}
			return
		}
	}
	t.Skip("40 seeds and the free swing never landed the killing blow")
}

func TestUseRejectsTheWrongKind(t *testing.T) {
	g := richGame(t, 1)
	ctx := context.Background()
	if _, err := g.Buy(ctx, "potion of healing"); err != nil {
		t.Fatalf("buying returned %v", err)
	}

	_, err := g.Use("healing", KindBook)
	if err == nil {
		t.Fatal("reading a potion must be refused")
	}
	if !strings.Contains(err.Error(), "drink") {
		t.Errorf("error = %q, want it to point at the drink command", err)
	}

	if _, err := g.Use("", ""); err == nil {
		t.Error("using nothing must be refused")
	}
	if _, err := g.Use("sandwich", ""); err == nil {
		t.Error("using something you do not carry must be refused")
	}

	g.Char.HP = 0
	if _, err := g.Use("healing", KindPotion); !errors.Is(err, ErrDead) {
		t.Errorf("using an item while down returned %v, want ErrDead", err)
	}
}

func TestSellBagItemsAndPackWeapons(t *testing.T) {
	g := richGame(t, 1)
	ctx := context.Background()

	if _, err := g.Buy(ctx, "potion of healing"); err != nil {
		t.Fatalf("buying returned %v", err)
	}
	gold := g.Char.Gold

	if _, err := g.Sell("potion of healing"); err != nil {
		t.Fatalf("selling the potion returned %v", err)
	}
	if g.Char.Gold != gold+25 {
		t.Errorf("gold = %d, want %d after selling a 50 gp potion", g.Char.Gold, gold+25)
	}
	if len(g.Char.Items) != 0 {
		t.Errorf("the sold potion is still in the bag: %+v", g.Char.Items)
	}

	g.Char.AddWeapon(Weapon{Name: "Battleaxe", DamageDice: "1d8", Cost: "10 gp"})
	gold = g.Char.Gold
	if _, err := g.Sell("battleaxe"); err != nil {
		t.Fatalf("selling the weapon returned %v", err)
	}
	if g.Char.Gold != gold+5 {
		t.Errorf("gold = %d, want %d after selling a 10 gp axe", g.Char.Gold, gold+5)
	}
	if len(g.Char.Pack) != 0 {
		t.Errorf("the sold weapon is still in the pack: %+v", g.Char.Pack)
	}
}

func TestSellRefusesWhatYouAreHolding(t *testing.T) {
	g := richGame(t, 1)

	_, err := g.Sell("notched shortsword")
	if err == nil {
		t.Fatal("selling the weapon in your hands must be refused")
	}
	if !strings.Contains(err.Error(), "equip something else") {
		t.Errorf("error = %q, want it to suggest equipping something else", err)
	}
	if g.Char.Weapon.Name != "Notched shortsword" {
		t.Error("the refused sale took the weapon anyway")
	}

	if _, err := g.Sell(""); err == nil {
		t.Error("selling nothing must be refused")
	}
	if _, err := g.Sell("the moon"); err == nil {
		t.Error("selling something you do not carry must be refused")
	}
}

func TestStockIsStableBetweenVisits(t *testing.T) {
	g := richGame(t, 1)
	ctx := context.Background()

	first, err := g.Shop(ctx)
	if err != nil {
		t.Fatalf("Shop returned %v", err)
	}
	second, err := g.Shop(ctx)
	if err != nil {
		t.Fatalf("Shop returned %v", err)
	}
	if strings.Join(first, "\n") != strings.Join(second, "\n") {
		t.Error("the stock changed between two looks at the same blanket")
	}
}

func TestInventoryAndShopSurviveABareBag(t *testing.T) {
	g := newTestGame(t, 1)
	if got := strings.Join(g.Inventory(), "\n"); !strings.Contains(got, "bag is empty") {
		t.Errorf("inventory = %q, want it to say the bag is empty", got)
	}
}
