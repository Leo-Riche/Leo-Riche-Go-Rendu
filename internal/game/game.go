package game

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/leoriche/grimhold/internal/ansi"
	"github.com/leoriche/grimhold/internal/dice"
	"github.com/leoriche/grimhold/internal/open5e"
	"github.com/leoriche/grimhold/internal/world"
)

// Errors the REPL turns into a readable message instead of a crash.
var (
	ErrNoEncounter = errors.New("nothing is attacking you")
	ErrDead        = errors.New("you are in no state to fight")
	ErrInEncounter = errors.New("something has your full attention")
	ErrNoSpells    = errors.New("you know no spells")
)

// prefetchTimeout bounds the background warm-up started by travel.
const prefetchTimeout = 15 * time.Second

// Kill is one line of the slain bestiary.
type Kill struct {
	Slug    string  `json:"slug"`
	Name    string  `json:"name"`
	Count   int     `json:"count"`
	CR      float64 `json:"cr"`
	CRLabel string  `json:"cr_label"`
}

// Game is the whole mutable state of a session. Everything the REPL does goes
// through a method here, and every method is safe to call in any order: an
// impossible move returns an error instead of panicking.
type Game struct {
	World  *world.World
	Client *open5e.Client
	Roller *dice.Roller

	Char        *Character
	Location    string
	Rested      bool
	Looted      bool
	Slain       map[string]*Kill
	BossesSlain map[string]bool

	Foe         *Foe
	RoundNumber int

	// bestiary pagination, not part of the save file
	nextPage   string
	prevPage   string
	pageLoaded bool

	// weapons is the API weapon table, fetched on first use
	weapons []open5e.Weapon

	wg sync.WaitGroup
}

// New starts a fresh game at the world's starting region.
func New(w *world.World, client *open5e.Client, roller *dice.Roller, char *Character) *Game {
	return &Game{
		World:       w,
		Client:      client,
		Roller:      roller,
		Char:        char,
		Location:    w.Start,
		Slain:       map[string]*Kill{},
		BossesSlain: map[string]bool{},
	}
}

// Close waits for background prefetches to finish and stops the cache reaper.
func (g *Game) Close() {
	g.wg.Wait()
	if g.Client != nil {
		g.Client.Close()
	}
}

// Region is the region the player is standing in.
func (g *Game) Region() *world.Region {
	r, err := g.World.Region(g.Location)
	if err != nil {
		// The location always comes from the world or from a validated save,
		// so this only happens if a save was hand-edited. Fall back to the
		// start rather than returning nil.
		g.Location = g.World.Start
		r, _ = g.World.Region(g.Location)
	}
	return r
}

// Prompt is the REPL prompt, which carries the essentials so the player never
// has to ask.
func (g *Game) Prompt() string {
	r := g.Region()
	state := fmt.Sprintf("%d/%d", g.Char.HP, g.Char.MaxHP)
	switch {
	case !g.Char.Alive():
		state = ansi.Red("down")
	case g.Foe != nil:
		state += " " + ansi.Red("!"+g.Foe.Name)
	}
	return fmt.Sprintf("%s %s %s ", ansi.Bold("grimhold"), ansi.Gray("["+r.Name+" "+state+"]"), ">")
}

// Map lists every known region, its danger and its exits.
func (g *Game) Map() []string {
	lines := []string{ansi.Bold("Known regions:")}
	for _, r := range g.World.Sorted() {
		marker := " "
		if r.ID == g.Location {
			marker = ansi.Green(">")
		}
		gate := ""
		if r.MinLevel > 0 {
			gate = ansi.Magenta(fmt.Sprintf("  (level %d+)", r.MinLevel))
		}
		lines = append(lines, strings.TrimRight(fmt.Sprintf("%s %-24s %s  danger %d %-12s%s",
			marker, r.ID, ansi.Danger(r.Danger), r.Danger, "("+r.DangerLabel()+")", gate), " "))
		lines = append(lines, fmt.Sprintf("    %s %s", ansi.Gray("exits:"), strings.Join(r.Exits, ", ")))
	}
	lines = append(lines, ansi.Gray("Travel with: travel <region>"))
	return lines
}

// Look describes the current region.
func (g *Game) Look() []string {
	r := g.Region()
	lines := []string{
		ansi.Bold(r.Name),
		r.Description,
		fmt.Sprintf("Danger %s %d (%s)", ansi.Danger(r.Danger), r.Danger, r.DangerLabel()),
		fmt.Sprintf("Exits: %s", strings.Join(r.Exits, ", ")),
		fmt.Sprintf("Things that live here: %s", strings.Join(r.Foes, ", ")),
	}
	if r.Boss != "" && !g.BossesSlain[r.Boss] {
		lines = append(lines, ansi.Magenta("Something far worse is waiting at the end of this place."))
	}
	if g.Rested {
		lines = append(lines, ansi.Gray("You have already rested here."))
	}
	if g.Foe != nil {
		lines = append(lines, "", ansi.Red("You are not alone:"), "  "+g.Foe.Describe())
	}
	return lines
}

// Travel moves the player along an exit of the current region.
func (g *Game) Travel(ctx context.Context, id string) ([]string, error) {
	if g.Foe != nil {
		return nil, fmt.Errorf("%w: you cannot stroll away from the %s, fight or flee", ErrInEncounter, g.Foe.Name)
	}
	id = open5e.Slugify(id)
	if id == "" {
		return nil, errors.New("travel where? try: map")
	}

	here := g.Region()
	if id == here.ID {
		return []string{fmt.Sprintf("You are already in %s.", here.Name)}, nil
	}

	target, err := g.World.Region(id)
	if err != nil {
		if errors.Is(err, world.ErrUnknownRegion) {
			return nil, fmt.Errorf("there is no region called %q, try: map", id)
		}
		return nil, fmt.Errorf("travel: %w", err)
	}
	if !here.Connects(id) {
		return nil, fmt.Errorf("no road runs from %s to %s. Exits: %s",
			here.Name, target.Name, strings.Join(here.Exits, ", "))
	}
	if target.MinLevel > 0 && g.Char.Level < target.MinLevel {
		return nil, fmt.Errorf("the way into %s is sealed to you; come back at level %d (you are %d)",
			target.Name, target.MinLevel, g.Char.Level)
	}

	g.Location = target.ID
	g.Rested = false
	g.Looted = false

	// Bonus: warm the cache for this region's foes in the background, so the
	// first explore does not wait on the network.
	g.prefetch(target.Foes, target.Boss)

	lines := []string{
		fmt.Sprintf("You travel to %s.", ansi.Bold(target.Name)),
		target.Description,
		fmt.Sprintf("Danger %s %d (%s)", ansi.Danger(target.Danger), target.Danger, target.DangerLabel()),
	}
	return lines, nil
}

// prefetch warms the monster cache concurrently. It is fire and forget: Close
// waits for it, and any failure is simply re-tried by whatever needs the data.
func (g *Game) prefetch(foes []string, boss string) {
	if g.Client == nil || (len(foes) == 0 && boss == "") {
		return
	}
	slugs := make([]string, 0, len(foes)+1)
	slugs = append(slugs, foes...)
	if boss != "" {
		slugs = append(slugs, boss)
	}

	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), prefetchTimeout)
		defer cancel()
		g.Client.PrefetchMonsters(ctx, slugs)
	}()
}

// Explore wanders the region and may turn up a foe. In a dangerous region it
// draws several candidates and keeps the worst of them.
func (g *Game) Explore(ctx context.Context) ([]string, error) {
	if g.Foe != nil {
		return nil, fmt.Errorf("%w: the %s is still standing", ErrInEncounter, g.Foe.Name)
	}
	if !g.Char.Alive() {
		return nil, ErrDead
	}

	r := g.Region()

	// The boss waits at the end of its region and is fought once.
	if r.Boss != "" && !g.BossesSlain[r.Boss] {
		return g.encounterBoss(ctx, r)
	}

	// Sometimes a region is simply empty.
	if g.Roller.Chance(20) {
		return []string{g.Roller.Pick([]string{
			"You walk a long circle and find nothing but ash and old bones.",
			"Something watched you from the treeline and decided you were not worth it.",
			"You find a boot. The foot is still in it. Nothing else happens.",
			"Quiet. The kind that makes you check behind you twice.",
		})}, nil
	}

	rolls := r.Rolls()
	picks := make([]string, 0, rolls)
	for i := 0; i < rolls; i++ {
		if slug := g.Roller.Pick(r.Foes); slug != "" {
			picks = append(picks, slug)
		}
	}
	if len(picks) == 0 {
		return nil, fmt.Errorf("region %q has no foes to roll", r.ID)
	}

	monsters, err := g.Client.MonstersConcurrently(ctx, picks)
	if err != nil {
		return nil, fmt.Errorf("the fog will not lift (Open5e unreachable): %w", err)
	}

	// Keep the worst of the draws.
	worst := monsters[0]
	for _, m := range monsters[1:] {
		if m.CR > worst.CR {
			worst = m
		}
	}

	foe, err := NewFoe(worst, false)
	if err != nil {
		return nil, fmt.Errorf("explore: %w", err)
	}
	g.Foe = foe
	g.RoundNumber = 0

	lines := []string{}
	if rolls > 1 {
		lines = append(lines, ansi.Gray(fmt.Sprintf("(danger %d: %d draws, the worst of them steps out)", r.Danger, rolls)))
	}
	lines = append(lines,
		fmt.Sprintf("%s %s", ansi.Red("Encounter!"), foe.Name+" comes out of the dark."),
		"  "+foe.Describe(),
		ansi.Gray("fight to trade blows, fight all to see it through, flee to run."),
	)
	return lines, nil
}

// Ambush is the encounter the background ambush ticker triggers: a single foe
// from the current region, with no chance of finding nothing. It is a quieter
// failure than explore, because an ambush that cannot reach the API should
// simply not happen rather than printing an error over the prompt.
func (g *Game) Ambush(ctx context.Context) ([]string, error) {
	if g.Foe != nil {
		return nil, ErrInEncounter
	}
	if !g.Char.Alive() {
		return nil, ErrDead
	}
	r := g.Region()
	if r.Danger < 1 {
		return nil, fmt.Errorf("nothing hunts in %s", r.Name)
	}

	slug := g.Roller.Pick(r.Foes)
	m, err := g.Client.Monster(ctx, slug)
	if err != nil {
		return nil, fmt.Errorf("ambush: %w", err)
	}
	foe, err := NewFoe(m, false)
	if err != nil {
		return nil, fmt.Errorf("ambush: %w", err)
	}
	g.Foe = foe
	g.RoundNumber = 0

	return []string{
		"",
		ansi.Red("AMBUSH! ") + g.Roller.Pick([]string{
			"Something drops out of the dark behind you.",
			"A hand closes on your ankle.",
			"You hear it a half second too late.",
			"The ground gives way and it is already on you.",
		}),
		"  " + foe.Describe(),
		ansi.Gray("fight or flee."),
	}, nil
}

func (g *Game) encounterBoss(ctx context.Context, r *world.Region) ([]string, error) {
	m, err := g.Client.Monster(ctx, r.Boss)
	if err != nil {
		return nil, fmt.Errorf("the boss will not wake (Open5e unreachable): %w", err)
	}
	foe, err := NewFoe(m, true)
	if err != nil {
		return nil, fmt.Errorf("explore: %w", err)
	}
	g.Foe = foe
	g.RoundNumber = 0
	return []string{
		ansi.Magenta("The hall goes silent."),
		fmt.Sprintf("%s rises to meet you. There is no second chance at this.", ansi.Bold(foe.Name)),
		"  " + foe.Describe(),
	}, nil
}

// Fight resolves one round of combat, or every round until someone drops when
// all is true. Victory and defeat are settled here.
func (g *Game) Fight(all bool) ([]string, error) {
	if g.Foe == nil {
		return nil, fmt.Errorf("%w: explore to find something to hit", ErrNoEncounter)
	}
	if !g.Char.Alive() {
		return nil, ErrDead
	}

	var lines []string
	for {
		rd, err := g.FightRound()
		if err != nil {
			return lines, err
		}
		foe := g.Foe
		lines = append(lines, rd.Lines(foe.Name, g.Char.Name, foe.HP, foe.MaxHP, g.Char.HP, g.Char.MaxHP)...)

		switch {
		case rd.FoeDown:
			return append(lines, g.victory()...), nil
		case rd.PlayerDown:
			return append(lines, g.defeat()...), nil
		case !all:
			return append(lines, ansi.Gray("fight again, fight all, or flee.")), nil
		}
	}
}

// RunAway is the flee command: one roll to break away, a free hit against you
// if it fails, and the full defeat flow if that hit kills you.
func (g *Game) RunAway() ([]string, error) {
	if g.Foe == nil {
		return nil, fmt.Errorf("%w: nothing to run from", ErrNoEncounter)
	}
	foeName := g.Foe.Name

	res, err := g.Flee()
	if err != nil {
		return nil, err
	}
	lines := res.Lines(foeName, g.Char.Name, g.Char.HP, g.Char.MaxHP)

	switch {
	case res.Escaped:
		g.Foe = nil
		g.RoundNumber = 0
	case res.PlayerDown:
		lines = append(lines, g.defeat()...)
	default:
		lines = append(lines, ansi.Gray("fight, or try to flee again."))
	}
	return lines, nil
}

// victory settles a win: experience, gold, a bestiary entry and sometimes a
// weapon off the body.
func (g *Game) victory() []string {
	foe := g.Foe
	g.Foe = nil
	g.RoundNumber = 0
	if foe == nil {
		return nil
	}

	lines := []string{ansi.Green(fmt.Sprintf("%s is dead.", foe.Name))}
	lines = append(lines, g.Char.GainXP(foe.XP)...)

	gold := g.Roller.D(10) + int(foe.CR*5)
	if foe.Boss {
		gold *= 3
	}
	g.Char.Gold += gold
	lines = append(lines, fmt.Sprintf("You pick %d gold out of the mess (%d gp carried).", gold, g.Char.Gold))

	g.recordKill(foe)
	if foe.Boss {
		g.BossesSlain[foe.Slug] = true
		lines = append(lines, ansi.Magenta("The region is yours. Nothing else here will meet your eye."))
	}
	if drop, ok := g.rollWeaponDrop(foe); ok {
		lines = append(lines, fmt.Sprintf("It dropped %s. %s",
			ansi.Cyan(drop.String()), ansi.Gray("(equip "+strings.ToLower(drop.Name)+")")))
	}
	return lines
}

// recordKill adds the foe to the slain bestiary.
func (g *Game) recordKill(foe *Foe) {
	if g.Slain == nil {
		g.Slain = map[string]*Kill{}
	}
	key := foe.Slug
	if key == "" {
		key = strings.ToLower(foe.Name)
	}
	if k, ok := g.Slain[key]; ok {
		k.Count++
		return
	}
	g.Slain[key] = &Kill{Slug: key, Name: foe.Name, Count: 1, CR: foe.CR, CRLabel: foe.CRLabel}
}

// rollWeaponDrop gives a one in four chance of looting a weapon, using the
// weapon table if it has already been fetched. A boss always drops something.
func (g *Game) rollWeaponDrop(foe *Foe) (Weapon, bool) {
	if len(g.weapons) == 0 {
		return Weapon{}, false
	}
	if !foe.Boss && !g.Roller.Chance(25) {
		return Weapon{}, false
	}
	w := g.pickWeapon(foe.CR)
	if !g.Char.AddWeapon(w) {
		return Weapon{}, false
	}
	return w, true
}

// pickWeapon draws a melee weapon from the API table, preferring bigger dice
// for tougher foes.
func (g *Game) pickWeapon(cr float64) Weapon {
	candidates := make([]open5e.Weapon, 0, len(g.weapons))
	for _, w := range g.weapons {
		if !w.Melee() || w.DamageDice == "" {
			continue
		}
		martial := strings.Contains(strings.ToLower(w.Category), "martial")
		if cr >= 3 || martial {
			candidates = append(candidates, w)
		}
	}
	if len(candidates) == 0 {
		candidates = g.weapons
	}
	w := candidates[g.Roller.Intn(len(candidates))]
	return Weapon{Name: w.Name, DamageDice: w.DamageDice, DamageType: w.DamageType, Cost: w.Cost}
}

// defeat settles a loss: the player wakes up at the start region with one hit
// point. Nothing is written to disk here, so dying can never damage a save.
func (g *Game) defeat() []string {
	foe := g.Foe
	g.Foe = nil
	g.RoundNumber = 0

	lost := g.Char.Gold / 10
	g.Char.Gold -= lost
	g.Char.HP = 1
	g.Location = g.World.Start
	g.Rested = false
	g.Looted = false
	g.Char.ResetSpells()

	name := "something"
	if foe != nil {
		name = foe.Name
	}
	start := g.Region()
	return []string{
		ansi.Red(fmt.Sprintf("The %s leaves you for dead.", name)),
		fmt.Sprintf("You wake up at %s with 1 hit point, %d gold lighter, and your pride in pieces.", start.Name, lost),
		ansi.Gray("rest to patch yourself up. Your save file is untouched."),
	}
}

// Rest restores every hit point and every spell, once per region visit.
func (g *Game) Rest() ([]string, error) {
	if g.Foe != nil {
		return nil, fmt.Errorf("%w: you cannot nap with a %s in the room", ErrInEncounter, g.Foe.Name)
	}
	if g.Rested {
		return nil, fmt.Errorf("you have already rested in %s; travel somewhere else first", g.Region().Name)
	}

	g.Rested = true
	healed := g.Char.FullHeal()
	g.Char.ResetSpells()

	lines := []string{fmt.Sprintf("You sleep badly and wake whole. %s",
		ansi.HP(g.Char.HP, g.Char.MaxHP, 20))}
	if healed == 0 {
		lines = []string{fmt.Sprintf("You were unhurt, but the rest clears your head. %s",
			ansi.HP(g.Char.HP, g.Char.MaxHP, 20))}
	}
	if len(g.Char.Spells) > 0 {
		lines = append(lines, ansi.Gray("Your spells are ready again."))
	}
	return lines, nil
}

// Loot scavenges the current region, once per visit.
func (g *Game) Loot(ctx context.Context) ([]string, error) {
	if g.Foe != nil {
		return nil, fmt.Errorf("%w: the %s objects", ErrInEncounter, g.Foe.Name)
	}
	if g.Looted {
		return nil, fmt.Errorf("you have already picked %s clean", g.Region().Name)
	}
	g.Looted = true

	if err := g.loadWeapons(ctx); err != nil {
		// Looting still works without the weapon table; only the weapon part
		// of the drop is lost.
		g.weapons = nil
	}

	r := g.Region()
	gold := g.Roller.D(6) + r.Danger*g.Roller.D(4)
	g.Char.Gold += gold
	lines := []string{fmt.Sprintf("You turn over what is left of %s and find %d gold (%d gp carried).",
		r.Name, gold, g.Char.Gold)}

	if len(g.weapons) > 0 && g.Roller.Chance(35+10*r.Danger) {
		w := g.pickWeapon(float64(r.Danger))
		if g.Char.AddWeapon(w) {
			lines = append(lines, fmt.Sprintf("Buried in the muck: %s. %s",
				ansi.Cyan(w.String()), ansi.Gray("(equip "+strings.ToLower(w.Name)+")")))
		}
	}
	return lines, nil
}

// loadWeapons fetches the weapon table once per cache window.
func (g *Game) loadWeapons(ctx context.Context) error {
	if len(g.weapons) > 0 {
		return nil
	}
	weapons, err := g.Client.Weapons(ctx)
	if err != nil {
		return fmt.Errorf("weapons: %w", err)
	}
	g.weapons = weapons
	return nil
}

// WeaponList prints the weapon table from the API alongside the pack, so the
// player can see what is worth looking for.
func (g *Game) WeaponList(ctx context.Context) ([]string, error) {
	if err := g.loadWeapons(ctx); err != nil {
		return nil, fmt.Errorf("the quartermaster is out (Open5e unreachable): %w", err)
	}

	lines := []string{fmt.Sprintf("In hand: %s", ansi.Cyan(g.Char.Weapon.String()))}
	if len(g.Char.Pack) == 0 {
		lines = append(lines, ansi.Gray("Your pack is empty. Loot regions and bodies to fill it."))
	} else {
		lines = append(lines, ansi.Bold("In your pack:"))
		for _, w := range g.Char.Pack {
			lines = append(lines, "  "+w.String())
		}
		lines = append(lines, ansi.Gray("equip <weapon> to swap."))
	}

	melee := make([]open5e.Weapon, 0, len(g.weapons))
	for _, w := range g.weapons {
		if w.Melee() && w.DamageDice != "" && w.Document == "wotc-srd" {
			melee = append(melee, w)
		}
	}
	sort.Slice(melee, func(i, j int) bool { return melee[i].Name < melee[j].Name })
	if len(melee) > 0 {
		lines = append(lines, "", ansi.Bold(fmt.Sprintf("Melee weapons known to exist (%d, from /v1/weapons/):", len(melee))))
		for _, w := range melee {
			lines = append(lines, fmt.Sprintf("  %-22s %-6s %-12s %s", w.Name, w.DamageDice, w.DamageType, w.Cost))
		}
	}
	return lines, nil
}

// Equip swaps in a weapon from the pack.
func (g *Game) Equip(name string) ([]string, error) {
	w, err := g.Char.Equip(name)
	if err != nil {
		return nil, err
	}
	return []string{fmt.Sprintf("You take up %s. Damage is now %s.",
		ansi.Cyan(w.String()), g.Char.DamageExpr())}, nil
}

// Cast spends a spell: damage spells need a target, healing spells do not.
// Each spell can only be cast once per rest.
func (g *Game) Cast(name string) ([]string, error) {
	if len(g.Char.Spells) == 0 {
		return nil, fmt.Errorf("%w: you chose none at the crossroads", ErrNoSpells)
	}
	if !g.Char.Alive() {
		return nil, ErrDead
	}
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("cast what? try: spells")
	}

	spell, ok := g.Char.Spell(name)
	if !ok {
		known := make([]string, 0, len(g.Char.Spells))
		for _, s := range g.Char.Spells {
			known = append(known, s.Name)
		}
		return nil, fmt.Errorf("you do not know %q. You know: %s", name, strings.Join(known, ", "))
	}
	if g.Char.SpellUsed(spell.Slug) {
		return nil, fmt.Errorf("%s is spent until you rest", spell.Name)
	}

	if spell.Heals {
		g.Char.MarkSpellUsed(spell.Slug)
		healed := g.Char.Heal(spell.Expr().Roll(g.Roller))
		return []string{
			fmt.Sprintf("You cast %s and knit yourself back together for %d. %s",
				ansi.Cyan(spell.Name), healed, ansi.HP(g.Char.HP, g.Char.MaxHP, 20)),
		}, nil
	}

	if g.Foe == nil {
		return nil, fmt.Errorf("%w: %s needs something to hit", ErrNoEncounter, spell.Name)
	}
	g.Char.MarkSpellUsed(spell.Slug)
	damage := spell.Expr().Roll(g.Roller)
	g.Foe.Hurt(damage)

	lines := []string{fmt.Sprintf("You cast %s for %s damage (%s).",
		ansi.Cyan(spell.Name), ansi.Bold(fmt.Sprintf("%d", damage)), spell.Expr())}
	if !g.Foe.Alive() {
		return append(lines, g.victory()...), nil
	}
	lines = append(lines, fmt.Sprintf("  %s %s", g.Foe.Name, ansi.HP(g.Foe.HP, g.Foe.MaxHP, 14)))

	// The spell takes your whole turn, so the foe answers.
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

// SpellList prints the spells the character picked and whether they are ready.
func (g *Game) SpellList() []string {
	if len(g.Char.Spells) == 0 {
		return []string{ansi.Gray("You know no spells. Start a new game without -no-spells to pick two.")}
	}
	lines := []string{ansi.Bold("Spells:")}
	for _, s := range g.Char.Spells {
		state := ansi.Green("ready")
		if g.Char.SpellUsed(s.Slug) {
			state = ansi.Gray("spent until you rest")
		}
		kind := "damage " + s.Dice
		if s.Heals {
			kind = "healing " + s.Dice
		}
		lines = append(lines, fmt.Sprintf("  %-20s level %d  %-14s [%s]", s.Name, s.Level, kind, state))
	}
	return lines
}

// Bestiary pages through the API monster list. direction is "", "next" or
// "prev".
func (g *Game) Bestiary(ctx context.Context, direction string) ([]string, error) {
	var pageURL string
	switch strings.ToLower(strings.TrimSpace(direction)) {
	case "", "first":
		pageURL = ""
	case "next":
		switch {
		case !g.pageLoaded:
			return nil, errors.New("open the bestiary first, with: bestiary")
		case g.nextPage == "":
			return nil, errors.New("you are on the last page of the bestiary")
		}
		pageURL = g.nextPage
	case "prev", "previous", "back":
		switch {
		case !g.pageLoaded:
			return nil, errors.New("open the bestiary first, with: bestiary")
		case g.prevPage == "":
			return nil, errors.New("you are on the first page of the bestiary")
		}
		pageURL = g.prevPage
	default:
		return nil, fmt.Errorf("bestiary takes no argument, or next, or prev (got %q)", direction)
	}

	page, err := g.Client.Monsters(ctx, pageURL)
	if err != nil {
		return nil, fmt.Errorf("the bestiary is closed (Open5e unreachable): %w", err)
	}
	g.nextPage, g.prevPage = page.Next, page.Previous
	g.pageLoaded = true

	lines := []string{ansi.Bold(fmt.Sprintf("Bestiary (%d monsters, ordered by challenge rating):", page.Count))}
	for _, m := range page.Results {
		monster := m
		lines = append(lines, "  "+monster.Line())
	}
	nav := []string{}
	if g.prevPage != "" {
		nav = append(nav, "bestiary prev")
	}
	if g.nextPage != "" {
		nav = append(nav, "bestiary next")
	}
	if len(nav) > 0 {
		lines = append(lines, ansi.Gray(strings.Join(nav, "  |  ")))
	}
	lines = append(lines, ansi.Gray("inspect <monster> for a full stat block."))
	return lines, nil
}

// Inspect prints a monster's stat block, falling back to a search when the
// name is not an exact slug.
func (g *Game) Inspect(ctx context.Context, name string) ([]string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("inspect what? try: inspect goblin")
	}

	m, err := g.Client.Monster(ctx, name)
	if err == nil {
		return strings.Split(m.StatBlock(), "\n"), nil
	}
	if !errors.Is(err, open5e.ErrNotFound) {
		return nil, fmt.Errorf("the page is torn out (Open5e unreachable): %w", err)
	}

	matches, serr := g.Client.SearchMonsters(ctx, name, 10)
	if serr != nil {
		return nil, fmt.Errorf("no monster called %q, and the index is unreachable: %w", name, serr)
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("nothing in the bestiary matches %q", name)
	}
	if len(matches) == 1 {
		return strings.Split(matches[0].StatBlock(), "\n"), nil
	}

	lines := []string{fmt.Sprintf("No monster is called exactly %q. Did you mean:", name)}
	for _, m := range matches {
		monster := m
		lines = append(lines, fmt.Sprintf("  %-32s %s", monster.Slug, monster.Name))
	}
	return lines, nil
}

// SlainList prints the kill bestiary.
func (g *Game) SlainList() []string {
	if len(g.Slain) == 0 {
		return []string{ansi.Gray("You have killed nothing. The bestiary of your deeds is a blank page.")}
	}

	kills := make([]*Kill, 0, len(g.Slain))
	total := 0
	for _, k := range g.Slain {
		kills = append(kills, k)
		total += k.Count
	}
	sort.Slice(kills, func(i, j int) bool {
		if kills[i].Count != kills[j].Count {
			return kills[i].Count > kills[j].Count
		}
		return kills[i].Name < kills[j].Name
	})

	lines := []string{ansi.Bold(fmt.Sprintf("Slain bestiary (%s, %s):",
		plural(total, "kill"), plural(len(kills), "species", "species")))}
	for _, k := range kills {
		lines = append(lines, fmt.Sprintf("  %-30s CR %-5s x%d", k.Name, k.CRLabel, k.Count))
	}
	return lines
}

// plural renders a count with its noun, e.g. "1 kill" or "3 kills". An
// explicit plural form can be given for nouns that do not take an s.
func plural(n int, noun string, pluralForm ...string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	if len(pluralForm) > 0 {
		return fmt.Sprintf("%d %s", n, pluralForm[0])
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// CacheStats prints the state of the API cache, reaper included.
func (g *Game) CacheStats() []string {
	s := g.Client.Cache().Stats()
	return []string{
		ansi.Bold("Open5e response cache:"),
		fmt.Sprintf("  entries %d   hits %d   misses %d   purged %d", s.Entries, s.Hits, s.Misses, s.Purged),
		fmt.Sprintf("  ttl %s, reaped every %s by a background goroutine", s.TTL, open5e.DefaultReapInterval),
	}
}
