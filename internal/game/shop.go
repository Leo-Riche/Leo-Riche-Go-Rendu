package game

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/leoriche/grimhold/internal/ansi"
	"github.com/leoriche/grimhold/internal/open5e"
	"github.com/leoriche/grimhold/internal/world"
)

// Errors the shop commands return when the player is in the wrong place.
var (
	ErrNoShop  = errors.New("nobody is trading here")
	ErrNoMoney = errors.New("you cannot afford that")
)

// maxWeaponsOnSale caps how much of the API weapon table one merchant lays
// out, so the shop listing stays readable.
const maxWeaponsOnSale = 10

// Shop lists what the merchant in the current region has on offer. The stock
// is built once per region and then kept, so prices do not shuffle between two
// looks at the same blanket.
func (g *Game) Shop(ctx context.Context) ([]string, error) {
	if g.Foe != nil {
		return nil, fmt.Errorf("%w: the %s is not interested in commerce", ErrInEncounter, g.Foe.Name)
	}

	r := g.Region()
	if r.Shop == nil {
		return nil, fmt.Errorf("%w in %s", ErrNoShop, r.Name)
	}

	stock, err := g.stockFor(ctx, r)
	if err != nil {
		return nil, err
	}

	lines := []string{ansi.Bold(r.Shop.Name)}
	if r.Shop.Greeting != "" {
		lines = append(lines, r.Shop.Greeting)
	}
	if len(stock) == 0 {
		lines = append(lines, ansi.Gray("The blanket is bare today."))
		return lines, nil
	}

	for i, item := range stock {
		afford := " "
		if g.Char.Gold < item.Price {
			afford = ansi.Gray("x")
		}
		lines = append(lines, fmt.Sprintf(" %s %2d) %-52s %5d gp", afford, i+1, item.Label(), item.Price))
	}
	lines = append(lines,
		fmt.Sprintf("You carry %s. %s", ansi.Cyan(fmt.Sprintf("%d gp", g.Char.Gold)),
			ansi.Gray("(x marks what is beyond your purse)")),
		ansi.Gray("buy <number or name>, sell <item>, inventory to see what you carry."))
	return lines, nil
}

// stockFor builds (and then remembers) the stock of a region's merchant.
func (g *Game) stockFor(ctx context.Context, r *world.Region) ([]Item, error) {
	if g.stock == nil {
		g.stock = map[string][]Item{}
	}
	if held, ok := g.stock[r.ID]; ok {
		return g.freshStock(held), nil
	}

	var stock []Item
	var failures []string

	if r.Shop.Deals("potions") {
		potions, err := g.potionsOnSale(ctx)
		if err != nil {
			failures = append(failures, "potions")
		}
		stock = append(stock, potions...)
	}
	if r.Shop.Deals("weapons") {
		weapons, err := g.weaponsOnSale(ctx)
		if err != nil {
			failures = append(failures, "weapons")
		}
		stock = append(stock, weapons...)
	}
	if r.Shop.Deals("books") {
		books, err := g.booksOnSale(ctx)
		if err != nil {
			failures = append(failures, "grimoires")
		}
		stock = append(stock, books...)
	}

	if len(stock) == 0 {
		return nil, fmt.Errorf("the %s has nothing to sell (Open5e unreachable for %s)",
			r.Shop.Name, strings.Join(failures, " and "))
	}
	// A partial failure is not cached, so the missing goods appear on the
	// next visit once the API answers again.
	if len(failures) == 0 {
		g.stock[r.ID] = stock
	}
	return g.freshStock(stock), nil
}

// freshStock drops the grimoires of spells the player has since learned.
func (g *Game) freshStock(stock []Item) []Item {
	out := make([]Item, 0, len(stock))
	for _, item := range stock {
		if item.Kind == KindBook && item.Spell != nil && g.Char.KnowsSpell(item.Spell.Slug) {
			continue
		}
		out = append(out, item)
	}
	return out
}

// potionsOnSale reads the healing potion table out of the Open5e magic item.
func (g *Game) potionsOnSale(ctx context.Context) ([]Item, error) {
	item, err := g.Client.MagicItem(ctx, HealingPotionSlug)
	if err != nil {
		return nil, fmt.Errorf("potions: %w", err)
	}
	return PotionsFrom(item)
}

// weaponsOnSale lays out the SRD melee weapons, cheapest first.
func (g *Game) weaponsOnSale(ctx context.Context) ([]Item, error) {
	if err := g.loadWeapons(ctx); err != nil {
		return nil, err
	}

	var out []Item
	for _, w := range g.weapons {
		if !w.Melee() || w.DamageDice == "" || w.Document != "wotc-srd" || w.Cost == "" {
			continue
		}
		weapon := Weapon{Name: w.Name, DamageDice: w.DamageDice, DamageType: w.DamageType, Cost: w.Cost}
		out = append(out, Item{
			Slug:  open5e.Slugify(w.Name),
			Name:  weapon.String(),
			Kind:  KindWeapon,
			Price: PriceFromCost(w.Cost),
			Count: 1,
			Note:  w.Category,
		})
	}
	// A fence lays out what is worth buying: the hardest hitters first, and
	// the cheapest of two weapons that hit alike.
	sort.Slice(out, func(i, j int) bool {
		di, dj := damageOf(out[i], g.weapons), damageOf(out[j], g.weapons)
		if di != dj {
			return di > dj
		}
		if out[i].Price != out[j].Price {
			return out[i].Price < out[j].Price
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > maxWeaponsOnSale {
		out = out[:maxWeaponsOnSale]
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("weapons: the rack is empty")
	}
	return out, nil
}

// damageOf is the average damage of a weapon on sale.
func damageOf(item Item, table []open5e.Weapon) float64 {
	return weaponFromStock(item, table).Expr().Average()
}

// booksOnSale turns the spells on offer into grimoires, cheapest first.
func (g *Game) booksOnSale(ctx context.Context) ([]Item, error) {
	spells := OfferSpells(ctx, g.Client)
	if len(spells) == 0 {
		return nil, fmt.Errorf("grimoires: no spells could be fetched")
	}

	out := make([]Item, 0, len(spells))
	for _, spell := range spells {
		out = append(out, BookFor(spell))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Price != out[j].Price {
			return out[i].Price < out[j].Price
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// Buy takes a number from the shop listing, or a name, and hands over the
// gold. A weapon goes into the pack, everything else into the bag.
func (g *Game) Buy(ctx context.Context, what string) ([]string, error) {
	if g.Foe != nil {
		return nil, fmt.Errorf("%w: you cannot shop with a %s on you", ErrInEncounter, g.Foe.Name)
	}
	what = strings.TrimSpace(what)
	if what == "" {
		return nil, errors.New("buy what? try: shop")
	}

	r := g.Region()
	if r.Shop == nil {
		return nil, fmt.Errorf("%w in %s", ErrNoShop, r.Name)
	}
	stock, err := g.stockFor(ctx, r)
	if err != nil {
		return nil, err
	}

	item, err := pickFromStock(stock, what)
	if err != nil {
		return nil, err
	}
	if g.Char.Gold < item.Price {
		return nil, fmt.Errorf("%w: %s costs %d gp and you carry %d",
			ErrNoMoney, item.Name, item.Price, g.Char.Gold)
	}

	g.Char.Gold -= item.Price
	lines := []string{fmt.Sprintf("You buy %s for %d gp (%d gp left).",
		ansi.Cyan(item.Name), item.Price, g.Char.Gold)}

	if item.Kind == KindWeapon {
		weapon := weaponFromStock(item, g.weapons)
		if !g.Char.AddWeapon(weapon) {
			// Refuse rather than take the money for nothing.
			g.Char.Gold += item.Price
			return nil, fmt.Errorf("you are already carrying %s", weapon.Name)
		}
		return append(lines, ansi.Gray("equip "+strings.ToLower(weapon.Name)+" to put it in your hands.")), nil
	}

	g.Char.AddItem(item)
	switch item.Kind {
	case KindPotion:
		lines = append(lines, ansi.Gray("drink it with: drink "+strings.ToLower(item.Name)))
	case KindBook:
		hint := strings.ToLower(item.Name)
		if item.Spell != nil {
			hint = strings.ToLower(item.Spell.Name)
		}
		lines = append(lines, ansi.Gray("read it with: read "+hint))
	}
	return lines, nil
}

// pickFromStock resolves "3", a full name or a fragment of one.
func pickFromStock(stock []Item, what string) (Item, error) {
	if n, err := strconv.Atoi(what); err == nil {
		if n < 1 || n > len(stock) {
			return Item{}, fmt.Errorf("there is no %d on the blanket (1 to %d)", n, len(stock))
		}
		return stock[n-1], nil
	}

	needle := strings.ToLower(what)
	for _, item := range stock {
		if strings.ToLower(item.Name) == needle || item.Slug == open5e.Slugify(what) {
			return item, nil
		}
	}
	var matches []Item
	for _, item := range stock {
		if strings.Contains(strings.ToLower(item.Label()), needle) {
			matches = append(matches, item)
		}
	}
	switch len(matches) {
	case 0:
		return Item{}, fmt.Errorf("nothing on this blanket matches %q; try: shop", what)
	case 1:
		return matches[0], nil
	default:
		names := make([]string, 0, len(matches))
		for _, m := range matches {
			names = append(names, m.Name)
		}
		return Item{}, fmt.Errorf("%q matches several things: %s", what, strings.Join(names, ", "))
	}
}

// weaponFromStock rebuilds the weapon behind a stock entry, using the API
// table so the dice are the real ones.
func weaponFromStock(item Item, table []open5e.Weapon) Weapon {
	for _, w := range table {
		if open5e.Slugify(w.Name) == item.Slug {
			return Weapon{Name: w.Name, DamageDice: w.DamageDice, DamageType: w.DamageType, Cost: w.Cost}
		}
	}
	// The listing renders a weapon as "Name (1d8 slashing)", so the name is
	// everything before the bracket.
	name, _, _ := strings.Cut(item.Name, " (")
	return Weapon{Name: strings.TrimSpace(name), DamageDice: "1d6", Cost: fmt.Sprintf("%d gp", item.Price)}
}

// Sell hands something over for half what it is worth. Anything in the bag
// can go, and any weapon in the pack, but never the weapon in your hands.
func (g *Game) Sell(what string) ([]string, error) {
	if g.Foe != nil {
		return nil, fmt.Errorf("%w: the %s is not buying", ErrInEncounter, g.Foe.Name)
	}
	what = strings.TrimSpace(what)
	if what == "" {
		return nil, errors.New("sell what? try: inventory")
	}

	r := g.Region()
	if r.Shop == nil {
		return nil, fmt.Errorf("%w in %s", ErrNoShop, r.Name)
	}

	if item, ok := g.Char.FindItem(what); ok {
		g.Char.TakeItem(item.Slug)
		price := SellPrice(item.Price)
		g.Char.Gold += price
		return []string{fmt.Sprintf("You hand over %s for %d gp (%d gp carried).",
			item.Name, price, g.Char.Gold)}, nil
	}

	needle := strings.ToLower(what)
	for i, w := range g.Char.Pack {
		if !strings.EqualFold(w.Name, what) && !strings.Contains(strings.ToLower(w.Name), needle) {
			continue
		}
		price := SellPrice(weaponPrice(w))
		g.Char.Pack = append(g.Char.Pack[:i:i], g.Char.Pack[i+1:]...)
		g.Char.Gold += price
		return []string{fmt.Sprintf("You hand over %s for %d gp (%d gp carried).",
			w.String(), price, g.Char.Gold)}, nil
	}

	if strings.Contains(strings.ToLower(g.Char.Weapon.Name), needle) {
		return nil, fmt.Errorf("you are holding %s; equip something else first", g.Char.Weapon.Name)
	}
	return nil, fmt.Errorf("you are not carrying anything called %q", what)
}

// Use consumes an item from the bag: a potion is drunk, a grimoire is read.
// Drinking in a fight costs you your turn, so the foe strikes back.
//
// want restricts the search to one kind of item, which is how "drink" and
// "read" tell a potion from a grimoire of the same name. An empty want
// accepts anything.
func (g *Game) Use(what string, want ItemKind) ([]string, error) {
	what = strings.TrimSpace(what)
	if what == "" {
		return nil, errors.New("use what? try: inventory")
	}
	if !g.Char.Alive() {
		return nil, ErrDead
	}

	item, ok := g.Char.FindItemKind(what, want)
	if !ok {
		// Point at the right verb when the name matches another kind.
		if other, found := g.Char.FindItem(what); found {
			return nil, fmt.Errorf("%s is a %s; try: %s %s",
				other.Name, other.Kind, verbFor(other.Kind), strings.ToLower(other.Name))
		}
		if want != "" {
			return nil, fmt.Errorf("you are carrying no %s called %q", want, what)
		}
		return nil, fmt.Errorf("you are not carrying anything called %q", what)
	}

	switch item.Kind {
	case KindPotion:
		return g.drink(item)
	case KindBook:
		return g.read(item)
	default:
		return nil, fmt.Errorf("%s is not something you can use; try: equip %s", item.Name, item.Name)
	}
}

// verbFor is the command that uses a kind of item.
func verbFor(kind ItemKind) string {
	switch kind {
	case KindPotion:
		return "drink"
	case KindBook:
		return "read"
	default:
		return "equip"
	}
}

// drink empties a healing potion.
func (g *Game) drink(item Item) ([]string, error) {
	if g.Char.HP >= g.Char.MaxHP {
		return nil, fmt.Errorf("you are unhurt; save the %s", item.Name)
	}
	g.Char.TakeItem(item.Slug)

	healed := g.Char.Heal(item.HealExpr().Roll(g.Roller))
	lines := []string{fmt.Sprintf("You drink the %s and regain %s. %s",
		ansi.Cyan(item.Name), ansi.Green(fmt.Sprintf("%d HP", healed)),
		ansi.HP(g.Char.HP, g.Char.MaxHP, 20))}

	if g.Foe == nil {
		return lines, nil
	}

	// Drinking takes your whole turn, so the foe gets a swing at you.
	hit := resolveAttack(g.Roller, g.Foe.Name, g.Char.Name, g.Foe.AttackName,
		g.Foe.AttackBonus, g.Foe.damage, g.Foe.rider, g.Char.ArmorClass())
	if hit.Hit {
		g.Char.Hurt(hit.Damage)
	}
	lines = append(lines, "  "+hit.Line())
	if !g.Char.Alive() {
		return append(lines, g.defeat()...), nil
	}
	return append(lines, fmt.Sprintf("  %s %s", g.Char.Name, ansi.HP(g.Char.HP, g.Char.MaxHP, 14))), nil
}

// read studies a grimoire, which teaches its spell for good and destroys the
// book. It cannot be done with something trying to bite you.
func (g *Game) read(item Item) ([]string, error) {
	if g.Foe != nil {
		return nil, fmt.Errorf("%w: you cannot study with a %s in the room", ErrInEncounter, g.Foe.Name)
	}
	if item.Spell == nil {
		return nil, fmt.Errorf("%s is unreadable; the pages are blank", item.Name)
	}
	if g.Char.KnowsSpell(item.Spell.Slug) {
		return nil, fmt.Errorf("you already know %s", item.Spell.Name)
	}

	g.Char.TakeItem(item.Slug)
	g.Char.LearnSpell(*item.Spell)
	return []string{
		fmt.Sprintf("You read %s by candlelight. The pages crumble as you finish.", ansi.Cyan(item.Name)),
		fmt.Sprintf("%s is yours for good. %s", ansi.Bold(item.Spell.Name),
			ansi.Gray("cast "+strings.ToLower(item.Spell.Name)+", once per rest.")),
	}, nil
}

// Inventory lists everything the character carries.
func (g *Game) Inventory() []string { return g.Char.BagLines() }
