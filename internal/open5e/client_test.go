package open5e

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Fixtures trimmed from real api.open5e.com responses, so the decoding these
// tests exercise is the decoding the game relies on. No test in this package
// touches the network.
const goblinJSON = `{
  "slug": "goblin",
  "name": "Goblin",
  "size": "Small",
  "type": "Humanoid",
  "subtype": "goblinoid",
  "alignment": "neutral evil",
  "armor_class": 15,
  "armor_desc": "leather armor, shield",
  "hit_points": 7,
  "hit_dice": "2d6",
  "speed": {"walk": 30},
  "strength": 8, "dexterity": 14, "constitution": 10,
  "intelligence": 10, "wisdom": 8, "charisma": 8,
  "senses": "darkvision 60 ft., passive Perception 9",
  "languages": "Common, Goblin",
  "challenge_rating": "1/4",
  "cr": 0.25,
  "actions": [
    {"name": "Scimitar", "desc": "Melee Weapon Attack: +4 to hit.", "attack_bonus": 4, "damage_dice": "1d6", "damage_bonus": 2},
    {"name": "Shortbow", "desc": "Ranged Weapon Attack: +4 to hit.", "attack_bonus": 4, "damage_dice": "1d8", "damage_bonus": 2}
  ],
  "bonus_actions": null,
  "legendary_actions": null,
  "special_abilities": [{"name": "Nimble Escape", "desc": "The goblin can take the Disengage action as a bonus action."}]
}`

const swarmJSON = `{
  "slug": "swarm-of-rats",
  "name": "Swarm of Rats",
  "armor_class": 10,
  "hit_points": 24,
  "challenge_rating": "1/4",
  "cr": 0.25,
  "speed": {"walk": 30, "climb": 30, "hover": true},
  "actions": ""
}`

const pageOneJSON = `{
  "count": 2,
  "next": "%s/v1/monsters/?limit=1&page=2",
  "previous": null,
  "results": [` + goblinJSON + `]
}`

const pageTwoJSON = `{
  "count": 2,
  "next": null,
  "previous": "%s/v1/monsters/?limit=1",
  "results": [` + swarmJSON + `]
}`

const weaponsJSON = `{
  "count": 2,
  "next": null,
  "previous": null,
  "results": [
    {"name": "Battleaxe", "slug": "battleaxe", "category": "Martial Melee Weapons", "cost": "10 gp",
     "damage_dice": "1d8", "damage_type": "slashing", "weight": "4 lb.", "properties": ["versatile (1d10)"],
     "document__slug": "wotc-srd"},
    {"name": "Longbow", "slug": "longbow", "category": "Martial Ranged Weapons", "cost": "50 gp",
     "damage_dice": "1d8", "damage_type": "piercing", "weight": "2 lb.", "properties": ["ammunition"],
     "document__slug": "wotc-srd"}
  ]
}`

const fireBoltJSON = `{
  "slug": "fire-bolt",
  "name": "Fire Bolt",
  "desc": "You hurl a mote of fire. On a hit, the target takes 1d10 fire damage.",
  "level": "Cantrip",
  "level_int": 0,
  "school": "evocation",
  "dnd_class": "Sorcerer, Wizard"
}`

const potionJSON = `{
  "slug": "potion-of-healing",
  "name": "Potion of Healing",
  "type": "Potion",
  "desc": "| Healing | Common | 2d4 + 2 |",
  "rarity": "varies",
  "requires_attunement": "",
  "document__slug": "wotc-srd"
}`

// testServer returns a server serving the fixtures above, a request counter,
// and a client wired to it.
func testServer(t *testing.T) (*httptest.Server, *atomic.Int64, *Client) {
	t.Helper()

	var requests atomic.Int64
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	count := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			w.Header().Set("Content-Type", "application/json")
			next(w, r)
		}
	}

	mux.HandleFunc("/v1/monsters/goblin/", count(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, goblinJSON)
	}))
	mux.HandleFunc("/v1/monsters/swarm-of-rats/", count(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, swarmJSON)
	}))
	mux.HandleFunc("/v1/monsters/nothing/", count(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"detail":"Not found."}`, http.StatusNotFound)
	}))
	mux.HandleFunc("/v1/monsters/broken/", count(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	mux.HandleFunc("/v1/monsters/garbage/", count(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"name": [}`)
	}))
	mux.HandleFunc("/v1/monsters/", count(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("search") != "" {
			fmt.Fprintf(w, `{"count":1,"next":null,"previous":null,"results":[%s]}`, goblinJSON)
			return
		}
		if r.URL.Query().Get("page") == "2" {
			fmt.Fprintf(w, pageTwoJSON, srv.URL)
			return
		}
		fmt.Fprintf(w, pageOneJSON, srv.URL)
	}))
	mux.HandleFunc("/v1/weapons/", count(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, weaponsJSON)
	}))
	mux.HandleFunc("/v1/spells/fire-bolt/", count(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, fireBoltJSON)
	}))
	mux.HandleFunc("/v1/magicitems/potion-of-healing/", count(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, potionJSON)
	}))
	mux.HandleFunc("/v1/magicitems/empty/", count(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"slug":"empty"}`)
	}))

	client := NewClient(
		WithBaseURL(srv.URL),
		WithCache(NewCache(time.Minute, time.Hour)),
		WithHTTPClient(&http.Client{Timeout: 5 * time.Second}),
	)
	t.Cleanup(client.Close)
	return srv, &requests, client
}

func TestClientMonsterDecodes(t *testing.T) {
	_, _, client := testServer(t)

	m, err := client.Monster(context.Background(), "goblin")
	if err != nil {
		t.Fatalf("Monster(goblin) returned %v", err)
	}

	if m.Name != "Goblin" {
		t.Errorf("Name = %q, want Goblin", m.Name)
	}
	if m.ArmorClass != 15 || m.HitPoints != 7 {
		t.Errorf("AC/HP = %d/%d, want 15/7", m.ArmorClass, m.HitPoints)
	}
	if m.CRString() != "1/4" || m.CR != 0.25 {
		t.Errorf("CR = %q / %v, want 1/4 / 0.25", m.CRString(), m.CR)
	}
	if len(m.Actions) != 2 {
		t.Fatalf("decoded %d actions, want 2", len(m.Actions))
	}
	if len(m.SpecialAbilities) != 1 {
		t.Errorf("decoded %d special abilities, want 1", len(m.SpecialAbilities))
	}
	if m.LegendaryActions != nil {
		t.Errorf("legendary_actions was null, want nil, got %v", m.LegendaryActions)
	}
}

func TestBestAttackPicksTheHardestHit(t *testing.T) {
	_, _, client := testServer(t)

	m, err := client.Monster(context.Background(), "goblin")
	if err != nil {
		t.Fatalf("Monster(goblin) returned %v", err)
	}
	action, ok := m.BestAttack()
	if !ok {
		t.Fatal("the goblin has two attacks but BestAttack reported none")
	}
	if action.Name != "Shortbow" {
		t.Errorf("BestAttack = %q, want Shortbow (1d8 beats 1d6)", action.Name)
	}
	if action.AttackBonus == nil || *action.AttackBonus != 4 {
		t.Errorf("attack bonus = %v, want 4", action.AttackBonus)
	}
}

func TestActionsAsEmptyStringIsTolerated(t *testing.T) {
	_, _, client := testServer(t)

	m, err := client.Monster(context.Background(), "swarm-of-rats")
	if err != nil {
		t.Fatalf("Monster(swarm-of-rats) returned %v", err)
	}
	if len(m.Actions) != 0 {
		t.Errorf("actions = %v, want none", m.Actions)
	}
	if _, ok := m.BestAttack(); ok {
		t.Error("a monster with no actions must report no best attack")
	}
	// The hover speed is a boolean, which must not break decoding.
	if block := m.StatBlock(); !strings.Contains(block, "hover") {
		t.Errorf("stat block does not mention hover:\n%s", block)
	}
}

func TestClientServesSecondCallFromCache(t *testing.T) {
	_, requests, client := testServer(t)
	ctx := context.Background()

	if _, err := client.Monster(ctx, "goblin"); err != nil {
		t.Fatalf("first fetch failed: %v", err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("after one fetch the server saw %d requests, want 1", got)
	}

	for i := 0; i < 5; i++ {
		if _, err := client.Monster(ctx, "goblin"); err != nil {
			t.Fatalf("cached fetch %d failed: %v", i, err)
		}
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("the server saw %d requests, want 1: the cache is not being used", got)
	}
	if stats := client.Cache().Stats(); stats.Hits != 5 {
		t.Errorf("cache hits = %d, want 5", stats.Hits)
	}
}

func TestClientNotFound(t *testing.T) {
	_, _, client := testServer(t)

	_, err := client.Monster(context.Background(), "nothing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want it to wrap ErrNotFound", err)
	}
}

func TestClientServerErrorAndBadJSON(t *testing.T) {
	_, _, client := testServer(t)
	ctx := context.Background()

	_, err := client.Monster(ctx, "broken")
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want a plain failure for a 500", err)
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error %q should mention the status", err)
	}

	if _, err := client.Monster(ctx, "garbage"); err == nil {
		t.Error("malformed JSON must be reported as an error")
	}
}

func TestClientPaginationFollowsNextAndPrevious(t *testing.T) {
	_, _, client := testServer(t)
	ctx := context.Background()

	first, err := client.Monsters(ctx, "")
	if err != nil {
		t.Fatalf("first page failed: %v", err)
	}
	if first.Count != 2 || len(first.Results) != 1 {
		t.Fatalf("first page = count %d, %d results; want 2 and 1", first.Count, len(first.Results))
	}
	if first.Next == "" {
		t.Fatal("the first page has no next URL")
	}
	if first.Previous != "" {
		t.Errorf("the first page should have no previous URL, got %q", first.Previous)
	}

	second, err := client.Monsters(ctx, first.Next)
	if err != nil {
		t.Fatalf("second page failed: %v", err)
	}
	if len(second.Results) != 1 || second.Results[0].Name != "Swarm of Rats" {
		t.Fatalf("second page = %+v, want the swarm", second.Results)
	}
	if second.Next != "" || second.Previous == "" {
		t.Errorf("last page should have a previous and no next, got next %q previous %q", second.Next, second.Previous)
	}

	back, err := client.Monsters(ctx, second.Previous)
	if err != nil {
		t.Fatalf("paging back failed: %v", err)
	}
	if len(back.Results) != 1 || back.Results[0].Name != "Goblin" {
		t.Errorf("paging back gave %+v, want the goblin", back.Results)
	}
}

func TestClientRefusesPaginationURLOnAnotherHost(t *testing.T) {
	_, requests, client := testServer(t)

	before := requests.Load()
	_, err := client.Monsters(context.Background(), "https://evil.example.com/v1/monsters/")
	if err == nil {
		t.Fatal("following a pagination URL on another host must be refused")
	}
	if !strings.Contains(err.Error(), "refusing") {
		t.Errorf("error %q should say it is refusing the URL", err)
	}
	if got := requests.Load(); got != before {
		t.Error("the client made a request despite refusing the URL")
	}
}

func TestSearchMonsters(t *testing.T) {
	_, _, client := testServer(t)

	got, err := client.SearchMonsters(context.Background(), "gob", 5)
	if err != nil {
		t.Fatalf("SearchMonsters returned %v", err)
	}
	if len(got) != 1 || got[0].Slug != "goblin" {
		t.Errorf("search gave %+v, want the goblin", got)
	}
	if _, err := client.SearchMonsters(context.Background(), "  ", 5); err == nil {
		t.Error("an empty query must be rejected")
	}
}

func TestMonstersConcurrentlyKeepsWhatCameBack(t *testing.T) {
	_, _, client := testServer(t)
	ctx := context.Background()

	got, err := client.MonstersConcurrently(ctx, []string{"goblin", "nothing", "swarm-of-rats"})
	if err != nil {
		t.Fatalf("MonstersConcurrently returned %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d monsters, want the 2 that exist", len(got))
	}

	if _, err := client.MonstersConcurrently(ctx, []string{"nothing"}); err == nil {
		t.Error("a request where everything failed must return an error")
	}
	if _, err := client.MonstersConcurrently(ctx, nil); err == nil {
		t.Error("an empty slug list must return an error")
	}
}

func TestPrefetchWarmsTheCache(t *testing.T) {
	_, requests, client := testServer(t)
	ctx := context.Background()

	// Duplicates must be collapsed: three slugs, two of them the same.
	client.PrefetchMonsters(ctx, []string{"goblin", "goblin", "swarm-of-rats"})
	if got := requests.Load(); got != 2 {
		t.Errorf("prefetch made %d requests, want 2", got)
	}

	if _, err := client.Monster(ctx, "goblin"); err != nil {
		t.Fatalf("Monster after prefetch failed: %v", err)
	}
	if got := requests.Load(); got != 2 {
		t.Errorf("the server saw %d requests, want 2: the prefetch did not warm the cache", got)
	}
}

func TestWeaponsDecode(t *testing.T) {
	_, _, client := testServer(t)

	weapons, err := client.Weapons(context.Background())
	if err != nil {
		t.Fatalf("Weapons returned %v", err)
	}
	if len(weapons) != 2 {
		t.Fatalf("decoded %d weapons, want 2", len(weapons))
	}

	var melee, ranged int
	for _, w := range weapons {
		if w.Melee() {
			melee++
		} else {
			ranged++
		}
	}
	if melee != 1 || ranged != 1 {
		t.Errorf("melee/ranged = %d/%d, want 1/1", melee, ranged)
	}
	if weapons[0].DamageDice != "1d8" || weapons[0].Cost != "10 gp" {
		t.Errorf("battleaxe decoded as %+v", weapons[0])
	}
}

func TestSpellDecode(t *testing.T) {
	_, _, client := testServer(t)

	spell, err := client.Spell(context.Background(), "Fire Bolt")
	if err != nil {
		t.Fatalf("Spell returned %v", err)
	}
	if spell.Name != "Fire Bolt" || spell.LevelInt != 0 {
		t.Errorf("spell decoded as %+v", spell)
	}

	got := client.SpellsConcurrently(context.Background(), []string{"fire-bolt", "missing-spell"})
	if len(got) != 1 {
		t.Errorf("SpellsConcurrently returned %d spells, want the 1 that exists", len(got))
	}
}

func TestMagicItemDecodes(t *testing.T) {
	_, _, client := testServer(t)
	ctx := context.Background()

	item, err := client.MagicItem(ctx, "Potion of Healing")
	if err != nil {
		t.Fatalf("MagicItem returned %v", err)
	}
	if item.Name != "Potion of Healing" || item.Type != "Potion" {
		t.Errorf("item decoded as %+v", item)
	}
	if !strings.Contains(item.Desc, "2d4 + 2") {
		t.Errorf("the healing table is missing from the description: %q", item.Desc)
	}

	if _, err := client.MagicItem(ctx, "ring-of-nothing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("an unknown item returned %v, want ErrNotFound", err)
	}
	if _, err := client.MagicItem(ctx, "empty"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a nameless item returned %v, want ErrNotFound", err)
	}
	if _, err := client.MagicItem(ctx, "   "); !errors.Is(err, ErrNotFound) {
		t.Errorf("an empty slug returned %v, want ErrNotFound", err)
	}
}

func TestContextCancellationIsReported(t *testing.T) {
	_, _, client := testServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Monster(ctx, "goblin"); err == nil {
		t.Error("a cancelled context must fail the request")
	}
}

func TestSlugify(t *testing.T) {
	tests := map[string]string{
		"goblin":              "goblin",
		"Goblin":              "goblin",
		"  Young Black  ":     "young-black",
		"young black dragon":  "young-black-dragon",
		"Will-o'-Wisp":        "will-o-wisp",
		"swarm_of_rats":       "swarm-of-rats",
		"":                    "",
		"   ":                 "",
		"!!!":                 "",
		"Mummy Lord (undead)": "mummy-lord-undead",
	}
	for in, want := range tests {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMonsterLineAndStatBlock(t *testing.T) {
	_, _, client := testServer(t)

	m, err := client.Monster(context.Background(), "goblin")
	if err != nil {
		t.Fatalf("Monster(goblin) returned %v", err)
	}

	line := m.Line()
	for _, want := range []string{"Goblin", "CR 1/4", "AC 15", "HP 7"} {
		if !strings.Contains(line, want) {
			t.Errorf("bestiary line %q does not contain %q", line, want)
		}
	}

	block := m.StatBlock()
	for _, want := range []string{"Goblin", "Armor Class  15", "Hit Points   7", "Scimitar", "Nimble Escape", "walk 30 ft."} {
		if !strings.Contains(block, want) {
			t.Errorf("stat block does not contain %q:\n%s", want, block)
		}
	}
}
