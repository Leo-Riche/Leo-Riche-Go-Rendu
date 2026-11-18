package world

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validWorld = `{
  "start": "crossroads",
  "regions": [
    {"id": "crossroads", "name": "The Crossroads", "description": "Four roads.",
     "danger": 0, "foes": ["giant-rat"], "exits": ["marsh"]},
    {"id": "marsh", "name": "Ashen Marsh", "description": "Fog and iron.",
     "danger": 3, "foes": ["goblin", "ghoul"], "exits": ["crossroads"]}
  ]
}`

func TestParseValidWorld(t *testing.T) {
	w, err := Parse([]byte(validWorld))
	if err != nil {
		t.Fatalf("Parse returned %v", err)
	}
	if w.Start != "crossroads" || len(w.Regions) != 2 {
		t.Fatalf("world = start %q with %d regions", w.Start, len(w.Regions))
	}

	r, err := w.Region("marsh")
	if err != nil {
		t.Fatalf("Region(marsh) returned %v", err)
	}
	if r.Name != "Ashen Marsh" || r.Danger != 3 {
		t.Errorf("marsh = %+v", r)
	}
	if !w.Has("CROSSROADS") {
		t.Error("Has should be case insensitive")
	}
	if w.Has("nowhere") {
		t.Error("Has(nowhere) must be false")
	}
}

func TestRegionUnknown(t *testing.T) {
	w, err := Parse([]byte(validWorld))
	if err != nil {
		t.Fatalf("Parse returned %v", err)
	}
	if _, err := w.Region("atlantis"); !errors.Is(err, ErrUnknownRegion) {
		t.Errorf("Region(atlantis) error = %v, want ErrUnknownRegion", err)
	}
}

func TestParseRejectsBrokenWorlds(t *testing.T) {
	tests := []struct {
		name string
		json string
		want string
	}{
		{
			name: "not json",
			json: `{`,
			want: "decode world",
		},
		{
			name: "no regions",
			json: `{"start": "x", "regions": []}`,
			want: "no regions",
		},
		{
			name: "start does not exist",
			json: `{"start": "nowhere", "regions": [{"id":"a","name":"A","danger":0,"foes":["rat"],"exits":[]}]}`,
			want: "start region",
		},
		{
			name: "no start",
			json: `{"regions": [{"id":"a","name":"A","danger":0,"foes":["rat"],"exits":[]}]}`,
			want: "no start region",
		},
		{
			name: "duplicate ids",
			json: `{"start":"a","regions":[
				{"id":"a","name":"A","danger":0,"foes":["rat"],"exits":[]},
				{"id":"a","name":"Other A","danger":0,"foes":["rat"],"exits":[]}]}`,
			want: "duplicate region id",
		},
		{
			name: "exit to nowhere",
			json: `{"start":"a","regions":[{"id":"a","name":"A","danger":0,"foes":["rat"],"exits":["b"]}]}`,
			want: "unknown region",
		},
		{
			name: "exit to itself",
			json: `{"start":"a","regions":[{"id":"a","name":"A","danger":0,"foes":["rat"],"exits":["a"]}]}`,
			want: "exit to itself",
		},
		{
			name: "missing id",
			json: `{"start":"a","regions":[{"name":"A","danger":0,"foes":["rat"],"exits":[]}]}`,
			want: "no id",
		},
		{
			name: "missing name",
			json: `{"start":"a","regions":[{"id":"a","danger":0,"foes":["rat"],"exits":[]}]}`,
			want: "no name",
		},
		{
			name: "no foes",
			json: `{"start":"a","regions":[{"id":"a","name":"A","danger":0,"foes":[],"exits":[]}]}`,
			want: "no foes",
		},
		{
			name: "negative danger",
			json: `{"start":"a","regions":[{"id":"a","name":"A","danger":-2,"foes":["rat"],"exits":[]}]}`,
			want: "negative danger",
		},
		{
			name: "shop with no name",
			json: `{"start":"a","regions":[{"id":"a","name":"A","danger":0,"foes":["rat"],"exits":[],
				"shop":{"sells":["potions"]}}]}`,
			want: "shop with no name",
		},
		{
			name: "shop that sells nothing",
			json: `{"start":"a","regions":[{"id":"a","name":"A","danger":0,"foes":["rat"],"exits":[],
				"shop":{"name":"Ash","sells":[]}}]}`,
			want: "sells nothing",
		},
		{
			name: "shop that sells the unknown",
			json: `{"start":"a","regions":[{"id":"a","name":"A","danger":0,"foes":["rat"],"exits":[],
				"shop":{"name":"Ash","sells":["dragons"]}}]}`,
			want: "unknown goods",
		},
		{
			name: "typo in a field name",
			json: `{"start":"a","regions":[{"id":"a","name":"A","danger":0,"foes":["rat"],"exit":["b"]}]}`,
			want: "unknown field",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.json))
			if err == nil {
				t.Fatalf("Parse accepted a broken world (%s)", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestSortedByDangerThenName(t *testing.T) {
	w, err := Parse([]byte(validWorld))
	if err != nil {
		t.Fatalf("Parse returned %v", err)
	}
	sorted := w.Sorted()
	if len(sorted) != 2 || sorted[0].ID != "crossroads" {
		t.Errorf("Sorted gave %v, want the quiet region first", sorted)
	}

	// Sorted must be a copy: changing it cannot corrupt the world.
	sorted[0].Name = "Vandalised"
	if r, _ := w.Region("crossroads"); r.Name != "The Crossroads" {
		t.Error("Sorted returned the live regions instead of a copy")
	}
}

func TestConnects(t *testing.T) {
	w, err := Parse([]byte(validWorld))
	if err != nil {
		t.Fatalf("Parse returned %v", err)
	}
	r, _ := w.Region("crossroads")

	if !r.Connects("marsh") {
		t.Error("crossroads should connect to marsh")
	}
	if !r.Connects("  MARSH ") {
		t.Error("Connects should trim and lowercase its argument")
	}
	if r.Connects("crossroads") {
		t.Error("crossroads must not connect to itself")
	}
}

func TestDangerLabelAndRolls(t *testing.T) {
	tests := []struct {
		danger int
		label  string
		rolls  int
	}{
		{0, "quiet", 1},
		{1, "uneasy", 1},
		{2, "dangerous", 2},
		{3, "deadly", 2},
		{4, "murderous", 3},
		{5, "hopeless", 3},
		{9, "hopeless", 5},
	}
	for _, tc := range tests {
		r := Region{Danger: tc.danger}
		if got := r.DangerLabel(); got != tc.label {
			t.Errorf("danger %d label = %q, want %q", tc.danger, got, tc.label)
		}
		if got := r.Rolls(); got != tc.rolls {
			t.Errorf("danger %d rolls = %d, want %d", tc.danger, got, tc.rolls)
		}
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err == nil {
		t.Fatal("loading a missing world must fail")
	}
	if !strings.Contains(err.Error(), "load world") {
		t.Errorf("error = %q, want it to name the operation", err)
	}
}

func TestLoadFromDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "world.json")
	if err := os.WriteFile(path, []byte(validWorld), 0o600); err != nil {
		t.Fatalf("writing the fixture failed: %v", err)
	}

	w, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned %v", err)
	}
	if w.Start != "crossroads" {
		t.Errorf("start = %q", w.Start)
	}
}

// TestShippedWorldIsValid parses the world.json that ships with the game, so a
// typo in the map is caught by the test suite rather than at startup.
func TestShippedWorldIsValid(t *testing.T) {
	path := filepath.Join("..", "..", "world.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("world.json not found at %s: %v", path, err)
	}

	w, err := Parse(data)
	if err != nil {
		t.Fatalf("the shipped world.json does not parse: %v", err)
	}
	if len(w.Regions) < 5 {
		t.Errorf("the shipped world has %d regions, the assignment asks for 5 or 6", len(w.Regions))
	}

	// Every exit should lead somewhere that leads back, or the map reads as
	// broken even though it validates.
	for _, r := range w.Regions {
		for _, exit := range r.Exits {
			other, err := w.Region(exit)
			if err != nil {
				t.Fatalf("region %q exits to %q: %v", r.ID, exit, err)
			}
			if !other.Connects(r.ID) {
				t.Errorf("%s leads to %s but %s does not lead back", r.ID, exit, exit)
			}
		}
	}

	// Every foe and boss must look like an API slug: lower case, no spaces.
	// A typo here would only surface as a failed fetch mid-game.
	for _, r := range w.Regions {
		names := append([]string{}, r.Foes...)
		if r.Boss != "" {
			names = append(names, r.Boss)
		}
		for _, name := range names {
			if name != strings.ToLower(name) || strings.ContainsAny(name, " _") {
				t.Errorf("region %q lists %q, which is not a slug", r.ID, name)
			}
		}
	}

	// The gated region must be reachable from somewhere.
	for _, r := range w.Regions {
		if r.MinLevel == 0 {
			continue
		}
		reachable := false
		for _, other := range w.Regions {
			if other.ID != r.ID && other.Connects(r.ID) {
				reachable = true
				break
			}
		}
		if !reachable {
			t.Errorf("gated region %q cannot be reached from anywhere", r.ID)
		}
	}
}

func TestShopDeals(t *testing.T) {
	const withShop = `{
	  "start": "a",
	  "regions": [{"id":"a","name":"A","description":"d","danger":0,"foes":["rat"],"exits":[],
	    "shop":{"name":"Mother Ash","greeting":"A blanket of bottles.","sells":["potions","books"]}}]
	}`

	w, err := Parse([]byte(withShop))
	if err != nil {
		t.Fatalf("Parse returned %v", err)
	}
	r, err := w.Region("a")
	if err != nil {
		t.Fatalf("Region returned %v", err)
	}
	if r.Shop == nil {
		t.Fatal("the shop was not decoded")
	}
	if r.Shop.Name != "Mother Ash" || r.Shop.Greeting == "" {
		t.Errorf("shop = %+v", r.Shop)
	}

	if !r.Shop.Deals("potions") || !r.Shop.Deals("books") {
		t.Error("the shop should deal in what it lists")
	}
	if r.Shop.Deals("weapons") {
		t.Error("the shop should not deal in what it does not list")
	}

	var none *Shop
	if none.Deals("potions") {
		t.Error("a region with no merchant deals in nothing")
	}
}

// TestShippedWorldHasMerchants keeps the shipped map honest: the money the
// player collects must have somewhere to go.
func TestShippedWorldHasMerchants(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "world.json"))
	if err != nil {
		t.Skipf("world.json not found: %v", err)
	}
	w, err := Parse(data)
	if err != nil {
		t.Fatalf("the shipped world does not parse: %v", err)
	}

	sold := map[string]bool{}
	shops := 0
	for _, r := range w.Regions {
		if r.Shop == nil {
			continue
		}
		shops++
		if r.Danger > 1 {
			t.Errorf("merchant %q stands in %q, danger %d: nobody trades in a place that deadly",
				r.Shop.Name, r.ID, r.Danger)
		}
		for _, kind := range r.Shop.Sells {
			sold[kind] = true
		}
	}
	if shops == 0 {
		t.Fatal("the shipped world has no merchant at all")
	}
	for _, kind := range ShopKinds {
		if !sold[kind] {
			t.Errorf("no merchant in the world sells %s", kind)
		}
	}
}

func TestDefaultPathPrefersTheFlag(t *testing.T) {
	if got := DefaultPath("custom/world.json"); got != "custom/world.json" {
		t.Errorf("DefaultPath = %q, want the flag value", got)
	}
	if got := DefaultPath(""); got == "" {
		t.Error("DefaultPath with no flag must still return a candidate path")
	}
}
