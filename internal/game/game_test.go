package game

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/leoriche/grimhold/internal/ansi"
	"github.com/leoriche/grimhold/internal/dice"
	"github.com/leoriche/grimhold/internal/open5e"
	"github.com/leoriche/grimhold/internal/world"
)

// The game tests never touch the network: every monster comes from the
// httptest server below.

const testWorldJSON = `{
  "start": "crossroads",
  "regions": [
    {"id": "crossroads", "name": "The Crossroads", "description": "Four roads.",
     "danger": 0, "foes": ["goblin"], "exits": ["marsh"],
     "shop": {"name": "Mother Ash", "greeting": "A blanket of bottles.", "sells": ["potions"]}},
    {"id": "marsh", "name": "Ashen Marsh", "description": "Fog and iron.",
     "danger": 1, "foes": ["goblin", "ogre"], "exits": ["crossroads", "vault"],
     "shop": {"name": "The Drowned Library", "greeting": "A punt of books.", "sells": ["books", "weapons"]}},
    {"id": "vault", "name": "Obsidian Vault", "description": "Black glass stairs.",
     "danger": 4, "min_level": 5, "boss": "mummy-lord",
     "foes": ["ogre"], "exits": ["marsh"]}
  ]
}`

const testGoblinJSON = `{
  "slug": "goblin", "name": "Goblin", "size": "Small", "type": "Humanoid",
  "armor_class": 15, "hit_points": 7, "hit_dice": "2d6",
  "challenge_rating": "1/4", "cr": 0.25, "speed": {"walk": 30},
  "actions": [{"name": "Scimitar", "desc": "+4 to hit", "attack_bonus": 4, "damage_dice": "1d6", "damage_bonus": 2}]
}`

const testOgreJSON = `{
  "slug": "ogre", "name": "Ogre", "size": "Large", "type": "Giant",
  "armor_class": 11, "hit_points": 59, "hit_dice": "7d10+21",
  "challenge_rating": "2", "cr": 2.0, "speed": {"walk": 40},
  "actions": [{"name": "Greatclub", "desc": "+6 to hit", "attack_bonus": 6, "damage_dice": "2d8", "damage_bonus": 4}]
}`

// The mummy lord is the interesting case: its damage expression carries a
// rider group that must not be treated as damage on an ordinary hit.
const testMummyLordJSON = `{
  "slug": "mummy-lord", "name": "Mummy Lord", "size": "Medium", "type": "Undead",
  "armor_class": 17, "hit_points": 97, "challenge_rating": "15", "cr": 15.0,
  "speed": {"walk": 20},
  "actions": [
    {"name": "Multiattack", "desc": "The mummy can use its Dreadful Glare and makes one attack."},
    {"name": "Rotting Fist", "desc": "+9 to hit", "attack_bonus": 9, "damage_dice": "3d6+6d6", "damage_bonus": 4}
  ],
  "legendary_actions": [{"name": "Attack", "desc": "The mummy lord makes one attack."}]
}`

const testSwarmJSON = `{
  "slug": "swarm-of-rats", "name": "Swarm of Rats",
  "armor_class": 10, "hit_points": 24, "challenge_rating": "1/4", "cr": 0.25,
  "actions": ""
}`

const testPotionJSON = `{
  "slug": "potion-of-healing",
  "name": "Potion of Healing",
  "type": "Potion",
  "desc": "You regain hit points when you drink this potion.\n\n**Potions of Healing (table)**\n\n| Potion of ...    | Rarity    | HP Regained |\n|------------------|-----------|-------------|\n| Healing          | Common    | 2d4 + 2     |\n| Greater healing  | Uncommon  | 4d4 + 4     |\n| Superior healing | Rare      | 8d4 + 8     |\n| Supreme healing  | Very rare | 10d4 + 20   |",
  "rarity": "varies",
  "document__slug": "wotc-srd"
}`

const testWeaponsJSON = `{
  "count": 2, "next": null, "previous": null,
  "results": [
    {"name": "Greatsword", "slug": "greatsword", "category": "Martial Melee Weapons",
     "cost": "50 gp", "damage_dice": "2d6", "damage_type": "slashing", "document__slug": "wotc-srd"},
    {"name": "Club", "slug": "club", "category": "Simple Melee Weapons",
     "cost": "1 sp", "damage_dice": "1d4", "damage_type": "bludgeoning", "document__slug": "wotc-srd"},
    {"name": "Longbow", "slug": "longbow", "category": "Martial Ranged Weapons",
     "cost": "50 gp", "damage_dice": "1d8", "damage_type": "piercing", "document__slug": "wotc-srd"}
  ]
}`

const testFireBoltJSON = `{
  "slug": "fire-bolt", "name": "Fire Bolt",
  "desc": "The target takes 1d10 fire damage.", "level": "Cantrip", "level_int": 0
}`

const testCureWoundsJSON = `{
  "slug": "cure-wounds", "name": "Cure Wounds",
  "desc": "A creature you touch regains 1d8 hit points.", "level": "1st-level", "level_int": 1
}`

// newTestGame builds a game whose client is aimed at a local server, with a
// fixed dice seed so every roll in the test is reproducible.
func newTestGame(t *testing.T, seed int64) *Game {
	t.Helper()

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	serve := func(path, body string) {
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, body)
		})
	}
	serve("/v1/monsters/goblin/", testGoblinJSON)
	serve("/v1/monsters/ogre/", testOgreJSON)
	serve("/v1/monsters/mummy-lord/", testMummyLordJSON)
	serve("/v1/monsters/swarm-of-rats/", testSwarmJSON)
	serve("/v1/weapons/", testWeaponsJSON)
	serve("/v1/magicitems/potion-of-healing/", testPotionJSON)
	serve("/v1/spells/fire-bolt/", testFireBoltJSON)
	serve("/v1/spells/cure-wounds/", testCureWoundsJSON)
	mux.HandleFunc("/v1/monsters/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/v1/monsters/" {
			// An unknown slug, exactly as the real API answers it.
			http.Error(w, `{"detail":"Not found."}`, http.StatusNotFound)
			return
		}
		if q := r.URL.Query().Get("search"); q != "" {
			fmt.Fprintf(w, `{"count":1,"next":null,"previous":null,"results":[%s]}`, testGoblinJSON)
			return
		}
		fmt.Fprintf(w, `{"count":2,"next":"%s/v1/monsters/?page=2","previous":null,"results":[%s,%s]}`,
			srv.URL, testGoblinJSON, testOgreJSON)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"detail":"Not found."}`, http.StatusNotFound)
	})

	client := open5e.NewClient(
		open5e.WithBaseURL(srv.URL),
		open5e.WithCache(open5e.NewCache(time.Minute, time.Hour)),
	)

	w, err := world.Parse([]byte(testWorldJSON))
	if err != nil {
		t.Fatalf("test world does not parse: %v", err)
	}

	g := New(w, client, dice.New(seed), NewCharacter("Tester"))
	t.Cleanup(g.Close)
	return g
}

func init() {
	// Keep test output free of escape codes.
	ansi.Enable(false)
}

func TestNewGameStartsAtTheStartRegion(t *testing.T) {
	g := newTestGame(t, 1)

	if g.Location != "crossroads" {
		t.Errorf("start location = %q, want crossroads", g.Location)
	}
	if g.Region().Name != "The Crossroads" {
		t.Errorf("start region = %q", g.Region().Name)
	}
	if g.Foe != nil {
		t.Error("a new game must not start in a fight")
	}
	if !strings.Contains(strings.Join(g.Look(), "\n"), "Four roads") {
		t.Error("look does not print the region description")
	}
	if !strings.Contains(strings.Join(g.Map(), "\n"), "obsidian") &&
		!strings.Contains(strings.Join(g.Map(), "\n"), "vault") {
		t.Error("map does not list every region")
	}
}

func TestTravelFollowsExitsOnly(t *testing.T) {
	g := newTestGame(t, 1)
	ctx := context.Background()

	if _, err := g.Travel(ctx, "marsh"); err != nil {
		t.Fatalf("travel to a connected region failed: %v", err)
	}
	if g.Location != "marsh" {
		t.Fatalf("location = %q, want marsh", g.Location)
	}

	// crossroads is not connected to vault.
	if _, err := g.Travel(ctx, "crossroads"); err != nil {
		t.Fatalf("travel back failed: %v", err)
	}
	if _, err := g.Travel(ctx, "vault"); err == nil {
		t.Error("travelling to a region with no road must be refused")
	}
	if g.Location != "crossroads" {
		t.Errorf("a refused travel moved the player to %q", g.Location)
	}

	if _, err := g.Travel(ctx, "atlantis"); err == nil {
		t.Error("travelling to an unknown region must be refused")
	}
	if _, err := g.Travel(ctx, ""); err == nil {
		t.Error("travel with no argument must be refused")
	}

	lines, err := g.Travel(ctx, "crossroads")
	if err != nil {
		t.Fatalf("travelling to where you already are returned %v", err)
	}
	if !strings.Contains(strings.Join(lines, " "), "already") {
		t.Errorf("expected an \"already here\" message, got %v", lines)
	}
}

func TestTravelRespectsTheLevelGate(t *testing.T) {
	g := newTestGame(t, 1)
	ctx := context.Background()

	if _, err := g.Travel(ctx, "marsh"); err != nil {
		t.Fatalf("travel to marsh failed: %v", err)
	}
	_, err := g.Travel(ctx, "vault")
	if err == nil {
		t.Fatal("a level 1 character must not be allowed into the gated region")
	}
	if !strings.Contains(err.Error(), "level 5") {
		t.Errorf("error = %q, want it to name the required level", err)
	}

	g.Char.Level = 5
	if _, err := g.Travel(ctx, "vault"); err != nil {
		t.Errorf("a level 5 character should be let in, got %v", err)
	}
}

func TestTravelRefusedMidFight(t *testing.T) {
	g := newTestGame(t, 1)
	ctx := context.Background()
	mustEncounter(t, g)

	if _, err := g.Travel(ctx, "marsh"); !errors.Is(err, ErrInEncounter) {
		t.Errorf("travel during a fight returned %v, want ErrInEncounter", err)
	}
}

// mustEncounter forces an encounter by exploring until one happens.
func mustEncounter(t *testing.T, g *Game) *Foe {
	t.Helper()
	for i := 0; i < 50; i++ {
		if _, err := g.Explore(context.Background()); err != nil {
			t.Fatalf("explore returned %v", err)
		}
		if g.Foe != nil {
			return g.Foe
		}
	}
	t.Fatal("50 explores in a row turned up nothing")
	return nil
}

func TestExploreEventuallyFindsAFoe(t *testing.T) {
	g := newTestGame(t, 5)
	foe := mustEncounter(t, g)

	if foe.Name == "" || foe.MaxHP <= 0 || foe.AC <= 0 {
		t.Errorf("encountered a malformed foe: %+v", foe)
	}
	if g.RoundNumber != 0 {
		t.Errorf("round counter = %d at the start of a fight, want 0", g.RoundNumber)
	}
	if _, err := g.Explore(context.Background()); !errors.Is(err, ErrInEncounter) {
		t.Error("exploring during a fight must be refused")
	}
}

func TestExploreInADangerousRegionKeepsTheWorstDraw(t *testing.T) {
	g := newTestGame(t, 3)
	ctx := context.Background()

	g.Char.Level = 5
	if _, err := g.Travel(ctx, "marsh"); err != nil {
		t.Fatalf("travel failed: %v", err)
	}
	if _, err := g.Travel(ctx, "vault"); err != nil {
		t.Fatalf("travel failed: %v", err)
	}
	// The vault's boss is fought first; clear it so the random draw runs.
	g.BossesSlain["mummy-lord"] = true

	// The vault only contains ogres, so the worst draw is always an ogre.
	foe := mustEncounter(t, g)
	if foe.Name != "Ogre" {
		t.Errorf("foe = %q, want Ogre", foe.Name)
	}
	if foe.MaxHP != 59 {
		t.Errorf("ogre HP = %d, want the stat block value 59", foe.MaxHP)
	}
}

func TestExploreMeetsTheBossFirstAndOnlyOnce(t *testing.T) {
	g := newTestGame(t, 2)
	ctx := context.Background()

	g.Char.Level = 5
	if _, err := g.Travel(ctx, "marsh"); err != nil {
		t.Fatalf("travel failed: %v", err)
	}
	if _, err := g.Travel(ctx, "vault"); err != nil {
		t.Fatalf("travel failed: %v", err)
	}

	lines, err := g.Explore(ctx)
	if err != nil {
		t.Fatalf("explore in the boss region returned %v", err)
	}
	if g.Foe == nil || !g.Foe.Boss {
		t.Fatalf("expected the boss, got %+v", g.Foe)
	}
	if g.Foe.Name != "Mummy Lord" {
		t.Errorf("boss = %q, want Mummy Lord", g.Foe.Name)
	}
	if !strings.Contains(strings.Join(lines, " "), "no second chance") {
		t.Errorf("boss encounter text = %v", lines)
	}

	// Kill it the quick way, then check it never comes back.
	g.Foe.HP = 0
	g.victory()
	if !g.BossesSlain["mummy-lord"] {
		t.Fatal("the boss was not recorded as slain")
	}
	for i := 0; i < 10; i++ {
		if _, err := g.Explore(ctx); err != nil {
			t.Fatalf("explore returned %v", err)
		}
		if g.Foe != nil && g.Foe.Boss {
			t.Fatal("the boss came back after being killed")
		}
		g.Foe = nil
	}
}

func TestRestOncePerRegionVisit(t *testing.T) {
	g := newTestGame(t, 1)
	ctx := context.Background()
	g.Char.Hurt(20)

	if _, err := g.Rest(); err != nil {
		t.Fatalf("the first rest failed: %v", err)
	}
	if g.Char.HP != g.Char.MaxHP {
		t.Errorf("HP after rest = %d, want %d", g.Char.HP, g.Char.MaxHP)
	}
	if _, err := g.Rest(); err == nil {
		t.Error("a second rest in the same region must be refused")
	}

	if _, err := g.Travel(ctx, "marsh"); err != nil {
		t.Fatalf("travel failed: %v", err)
	}
	if _, err := g.Rest(); err != nil {
		t.Errorf("resting after travelling failed: %v", err)
	}
}

func TestRestRefusedMidFight(t *testing.T) {
	g := newTestGame(t, 1)
	mustEncounter(t, g)
	if _, err := g.Rest(); !errors.Is(err, ErrInEncounter) {
		t.Errorf("rest during a fight returned %v, want ErrInEncounter", err)
	}
}

func TestLootOncePerVisitAndFillsThePack(t *testing.T) {
	g := newTestGame(t, 4)
	ctx := context.Background()

	goldBefore := g.Char.Gold
	if _, err := g.Loot(ctx); err != nil {
		t.Fatalf("loot returned %v", err)
	}
	if g.Char.Gold <= goldBefore {
		t.Errorf("gold = %d, want more than %d", g.Char.Gold, goldBefore)
	}
	if _, err := g.Loot(ctx); err == nil {
		t.Error("looting the same region twice must be refused")
	}

	// Looting enough regions should eventually turn up a weapon.
	found := false
	for i := 0; i < 20 && !found; i++ {
		target := "marsh"
		if g.Location == "marsh" {
			target = "crossroads"
		}
		if _, err := g.Travel(ctx, target); err != nil {
			t.Fatalf("travel failed: %v", err)
		}
		if _, err := g.Loot(ctx); err != nil {
			t.Fatalf("loot returned %v", err)
		}
		found = len(g.Char.Pack) > 0
	}
	if !found {
		t.Error("20 loots turned up no weapon at all")
	}
}

func TestEquipFromThePack(t *testing.T) {
	g := newTestGame(t, 1)

	if _, err := g.Equip("greatsword"); err == nil {
		t.Error("equipping something you do not carry must fail")
	}

	g.Char.AddWeapon(Weapon{Name: "Greatsword", DamageDice: "2d6", DamageType: "slashing"})
	lines, err := g.Equip("greatsword")
	if err != nil {
		t.Fatalf("equip returned %v", err)
	}
	if g.Char.Weapon.Name != "Greatsword" {
		t.Errorf("weapon = %q, want Greatsword", g.Char.Weapon.Name)
	}
	if !strings.Contains(strings.Join(lines, " "), "2d6") {
		t.Errorf("equip output %v should mention the new damage", lines)
	}
	// The old weapon goes back into the pack rather than vanishing.
	if len(g.Char.Pack) != 1 || g.Char.Pack[0].Name != "Notched shortsword" {
		t.Errorf("pack = %+v, want the old weapon in it", g.Char.Pack)
	}
}

func TestWeaponListMentionsWhatYouCarry(t *testing.T) {
	g := newTestGame(t, 1)

	lines, err := g.WeaponList(context.Background())
	if err != nil {
		t.Fatalf("WeaponList returned %v", err)
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"Notched shortsword", "Greatsword", "2d6"} {
		if !strings.Contains(joined, want) {
			t.Errorf("weapon list does not mention %q:\n%s", want, joined)
		}
	}
}

func TestBestiaryPaging(t *testing.T) {
	g := newTestGame(t, 1)
	ctx := context.Background()

	// Before any page has been opened, next and prev say so rather than
	// claiming you are at one end of the list.
	for _, dir := range []string{"next", "prev"} {
		_, err := g.Bestiary(ctx, dir)
		if err == nil {
			t.Fatalf("bestiary %s before opening the bestiary must be refused", dir)
		}
		if !strings.Contains(err.Error(), "open the bestiary first") {
			t.Errorf("bestiary %s error = %q, want it to say to open the bestiary first", dir, err)
		}
	}
	if _, err := g.Bestiary(ctx, "sideways"); err == nil {
		t.Error("an unknown direction must be refused")
	}

	lines, err := g.Bestiary(ctx, "")
	if err != nil {
		t.Fatalf("bestiary returned %v", err)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Goblin") || !strings.Contains(joined, "Ogre") {
		t.Errorf("bestiary page does not list the monsters:\n%s", joined)
	}
	if !strings.Contains(joined, "bestiary next") {
		t.Error("a page with a next URL should offer bestiary next")
	}

	// Now that a page is open, paging back is refused for the right reason.
	_, err = g.Bestiary(ctx, "prev")
	if err == nil {
		t.Fatal("paging back from the first page must be refused")
	}
	if !strings.Contains(err.Error(), "first page") {
		t.Errorf("error = %q, want it to mention the first page", err)
	}
}

func TestInspectKnownAndUnknownMonsters(t *testing.T) {
	g := newTestGame(t, 1)
	ctx := context.Background()

	lines, err := g.Inspect(ctx, "Goblin")
	if err != nil {
		t.Fatalf("inspect goblin returned %v", err)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Armor Class  15") {
		t.Errorf("stat block looks wrong:\n%s", joined)
	}

	if _, err := g.Inspect(ctx, ""); err == nil {
		t.Error("inspect with no argument must be refused")
	}

	// An unknown name falls back to the search endpoint.
	lines, err = g.Inspect(ctx, "gob")
	if err != nil {
		t.Fatalf("inspect fallback returned %v", err)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "Goblin") {
		t.Errorf("the search fallback did not find the goblin: %v", lines)
	}
}

func TestSlainListCountsKills(t *testing.T) {
	g := newTestGame(t, 1)

	if !strings.Contains(strings.Join(g.SlainList(), " "), "killed nothing") {
		t.Error("an empty bestiary should say so")
	}

	foe := &Foe{Slug: "goblin", Name: "Goblin", CR: 0.25, CRLabel: "1/4"}
	g.recordKill(foe)
	g.recordKill(foe)
	g.recordKill(&Foe{Slug: "ogre", Name: "Ogre", CR: 2, CRLabel: "2"})

	joined := strings.Join(g.SlainList(), "\n")
	if !strings.Contains(joined, "3 kills, 2 species") {
		t.Errorf("slain header is wrong:\n%s", joined)
	}
	if !strings.Contains(joined, "x2") {
		t.Errorf("the goblin count is missing:\n%s", joined)
	}
}

func TestCastSpells(t *testing.T) {
	g := newTestGame(t, 6)
	ctx := context.Background()

	if _, err := g.Cast("fire bolt"); !errors.Is(err, ErrNoSpells) {
		t.Errorf("casting with no spells returned %v, want ErrNoSpells", err)
	}

	g.Char.Spells = OfferSpells(ctx, g.Client)
	if len(g.Char.Spells) != 2 {
		t.Fatalf("the test server offers 2 spells, got %d", len(g.Char.Spells))
	}

	if _, err := g.Cast("ice knife"); err == nil {
		t.Error("casting an unknown spell must be refused")
	}
	if _, err := g.Cast("fire bolt"); !errors.Is(err, ErrNoEncounter) {
		t.Errorf("a damage spell with no target returned %v, want ErrNoEncounter", err)
	}

	// Healing works out of combat.
	g.Char.Hurt(20)
	before := g.Char.HP
	if _, err := g.Cast("cure wounds"); err != nil {
		t.Fatalf("cure wounds returned %v", err)
	}
	if g.Char.HP <= before {
		t.Errorf("HP = %d, want more than %d", g.Char.HP, before)
	}
	if _, err := g.Cast("cure wounds"); err == nil {
		t.Error("a spent spell must not be castable again before a rest")
	}

	if _, err := g.Rest(); err != nil {
		t.Fatalf("rest returned %v", err)
	}
	if g.Char.SpellUsed("cure-wounds") {
		t.Error("resting must make spells ready again")
	}

	// Damage works in combat.
	mustEncounter(t, g)
	hpBefore := g.Foe.HP
	if _, err := g.Cast("fire bolt"); err != nil {
		t.Fatalf("fire bolt returned %v", err)
	}
	if g.Foe != nil && g.Foe.HP >= hpBefore {
		t.Errorf("the foe took no damage from the spell: %d -> %d", hpBefore, g.Foe.HP)
	}
}

func TestCacheStatsReported(t *testing.T) {
	g := newTestGame(t, 1)
	if _, err := g.Client.Monster(context.Background(), "goblin"); err != nil {
		t.Fatalf("fetch failed: %v", err)
	}

	joined := strings.Join(g.CacheStats(), "\n")
	for _, want := range []string{"entries", "ttl", "goroutine"} {
		if !strings.Contains(joined, want) {
			t.Errorf("cache stats do not mention %q:\n%s", want, joined)
		}
	}
}

func TestPromptCarriesLocationAndHP(t *testing.T) {
	g := newTestGame(t, 1)
	if got := g.Prompt(); !strings.Contains(got, "The Crossroads") || !strings.Contains(got, "30/30") {
		t.Errorf("prompt = %q, want the region and the hit points", got)
	}

	mustEncounter(t, g)
	if got := g.Prompt(); !strings.Contains(got, g.Foe.Name) {
		t.Errorf("prompt = %q, want it to warn about the foe", got)
	}
}

func TestRegionFallsBackWhenTheSaveIsNonsense(t *testing.T) {
	g := newTestGame(t, 1)
	g.Location = "a region that does not exist"

	if got := g.Region(); got.ID != "crossroads" {
		t.Errorf("Region() = %q, want a fallback to the start region", got.ID)
	}
	if g.Location != "crossroads" {
		t.Errorf("location = %q, want it repaired to crossroads", g.Location)
	}
}
