package game

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "save.json")

	g := newTestGame(t, 1)
	g.Char.Name = "Leo"
	g.Char.GainXP(500)
	g.Char.Gold = 123
	g.Char.AddWeapon(Weapon{Name: "Greatsword", DamageDice: "2d6", DamageType: "slashing"})
	g.Char.Spells = []Spell{{Slug: "fire-bolt", Name: "Fire Bolt", Dice: "1d10"}}
	g.Char.MarkSpellUsed("fire-bolt")
	g.Location = "marsh"
	g.Rested = true
	g.Looted = true
	g.BossesSlain["mummy-lord"] = true
	g.recordKill(&Foe{Slug: "goblin", Name: "Goblin", CR: 0.25, CRLabel: "1/4"})
	g.recordKill(&Foe{Slug: "goblin", Name: "Goblin", CR: 0.25, CRLabel: "1/4"})

	if _, err := g.Save(path); err != nil {
		t.Fatalf("Save returned %v", err)
	}
	if !SaveExists(path) {
		t.Fatal("SaveExists says the file it just wrote is missing")
	}

	// A fresh game loads the file back.
	restored := newTestGame(t, 2)
	if _, err := restored.Load(path); err != nil {
		t.Fatalf("Load returned %v", err)
	}

	switch {
	case restored.Char.Name != "Leo":
		t.Errorf("name = %q", restored.Char.Name)
	case restored.Char.XP != 500:
		t.Errorf("XP = %d, want 500", restored.Char.XP)
	case restored.Char.Level != g.Char.Level:
		t.Errorf("level = %d, want %d", restored.Char.Level, g.Char.Level)
	case restored.Char.Gold != 123:
		t.Errorf("gold = %d, want 123", restored.Char.Gold)
	case restored.Location != "marsh":
		t.Errorf("location = %q, want marsh", restored.Location)
	case !restored.Rested || !restored.Looted:
		t.Error("the rest and loot flags were not restored")
	case !restored.BossesSlain["mummy-lord"]:
		t.Error("the slain boss was not restored")
	case restored.Slain["goblin"] == nil || restored.Slain["goblin"].Count != 2:
		t.Errorf("slain bestiary = %+v", restored.Slain)
	case len(restored.Char.Pack) != 1 || restored.Char.Pack[0].Name != "Greatsword":
		t.Errorf("pack = %+v", restored.Char.Pack)
	case !restored.Char.SpellUsed("fire-bolt"):
		t.Error("the spent spell was not restored as spent")
	}
}

func TestSaveAndLoadAPendingEncounter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "save.json")

	g := newTestGame(t, 1)
	foe := mustEncounter(t, g)
	foe.Hurt(2)
	wantHP := foe.HP

	if _, err := g.Save(path); err != nil {
		t.Fatalf("Save returned %v", err)
	}

	restored := newTestGame(t, 2)
	lines, err := restored.Load(path)
	if err != nil {
		t.Fatalf("Load returned %v", err)
	}
	if restored.Foe == nil {
		t.Fatal("the pending encounter was not restored")
	}
	if restored.Foe.HP != wantHP {
		t.Errorf("foe HP = %d, want %d", restored.Foe.HP, wantHP)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "unfinished") {
		t.Errorf("Load should mention the unfinished fight: %v", lines)
	}

	// The dice expressions are rebuilt on load, so the fight can continue.
	if _, err := restored.FightRound(); err != nil {
		t.Errorf("fighting a restored foe returned %v", err)
	}
}

func TestLoadMissingFileIsNotACrash(t *testing.T) {
	g := newTestGame(t, 1)
	path := filepath.Join(t.TempDir(), "nope.json")

	_, err := g.Load(path)
	if !errors.Is(err, ErrNoSave) {
		t.Fatalf("error = %v, want ErrNoSave", err)
	}
	if SaveExists(path) {
		t.Error("SaveExists lied about a missing file")
	}
}

func TestLoadRejectsBrokenSavesWithoutTouchingTheGame(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "not json", body: "{", want: "not a Grimhold save"},
		{name: "wrong version", body: `{"version": 99, "location": "crossroads"}`, want: "save version"},
		{name: "no character", body: `{"version": 1, "location": "crossroads"}`, want: "no character"},
		{
			name: "nameless character",
			body: `{"version":1,"location":"crossroads","character":{"level":1,"max_hp":10}}`,
			want: "no name",
		},
		{
			name: "no hit points",
			body: `{"version":1,"location":"crossroads","character":{"name":"X","level":1,"max_hp":0}}`,
			want: "no hit points",
		},
		{
			name: "no level",
			body: `{"version":1,"location":"crossroads","character":{"name":"X","level":0,"max_hp":10}}`,
			want: "no level",
		},
		{
			name: "region this world does not have",
			body: `{"version":1,"location":"atlantis","character":{"name":"X","level":1,"max_hp":10}}`,
			want: "does not contain",
		},
		{
			name: "unreadable pending foe",
			body: `{"version":1,"location":"crossroads","character":{"name":"X","level":1,"max_hp":10},
				"foe":{"name":"Thing","hp":5,"max_hp":5,"damage_dice":"gnaw"}}`,
			want: "saved encounter is unreadable",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "save.json")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatalf("writing the fixture failed: %v", err)
			}

			g := newTestGame(t, 1)
			g.Char.Name = "Original"
			g.Char.Gold = 42
			g.Location = "marsh"

			_, err := g.Load(path)
			if err == nil {
				t.Fatal("a broken save must be refused")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}

			// The running game must be exactly as it was.
			if g.Char.Name != "Original" || g.Char.Gold != 42 || g.Location != "marsh" {
				t.Errorf("a failed load changed the game: %q, %d gold, in %q",
					g.Char.Name, g.Char.Gold, g.Location)
			}
		})
	}
}

func TestLoadRepairsHarmlessInconsistencies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "save.json")
	const body = `{"version":1,"location":"crossroads",
		"character":{"name":"X","level":1,"hp":999,"max_hp":30,"attack_bonus":4,
		"weapon":{"name":"","damage_dice":""}}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the fixture failed: %v", err)
	}

	g := newTestGame(t, 1)
	if _, err := g.Load(path); err != nil {
		t.Fatalf("Load returned %v", err)
	}
	if g.Char.HP != 30 {
		t.Errorf("HP = %d, want it clamped to the maximum", g.Char.HP)
	}
	if g.Char.Weapon.DamageDice == "" {
		t.Error("a save with no weapon should fall back to the starting weapon")
	}
	if g.Char.SpellsUsed == nil {
		t.Error("the spell map should never be nil after a load")
	}
}

// TestSaveIsAtomic checks the promise the assignment makes: a failed write
// must leave the previous save intact.
func TestSaveIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "save.json")

	g := newTestGame(t, 1)
	g.Char.Name = "First"
	if _, err := g.Save(path); err != nil {
		t.Fatalf("the first save failed: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the first save failed: %v", err)
	}

	// Writing into a directory that does not exist fails at the temporary
	// file stage, before anything is renamed over the real save.
	g.Char.Name = "Second"
	if _, err := g.Save(filepath.Join(dir, "missing-dir", "save.json")); err == nil {
		t.Fatal("saving into a missing directory should fail")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the save after the failed write: %v", err)
	}
	if string(before) != string(after) {
		t.Error("a failed save changed the existing file")
	}
	if !strings.Contains(string(after), "First") {
		t.Error("the existing save no longer holds the original character")
	}

	// No temporary files are left lying around.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("listing the directory failed: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".grimhold-save-") {
			t.Errorf("a temporary file was left behind: %s", e.Name())
		}
	}
}

func TestSaveOverwritesAndStaysValidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "save.json")
	g := newTestGame(t, 1)

	for i := 0; i < 3; i++ {
		g.Char.Gold = 10 * i
		if _, err := g.Save(path); err != nil {
			t.Fatalf("save %d failed: %v", i, err)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the save failed: %v", err)
	}
	var snapshot SaveFile
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatalf("the save is not valid JSON: %v", err)
	}
	if snapshot.Character.Gold != 20 {
		t.Errorf("gold = %d, want the last write (20)", snapshot.Character.Gold)
	}
	if snapshot.Version != saveVersion {
		t.Errorf("version = %d, want %d", snapshot.Version, saveVersion)
	}
	if snapshot.SavedAt.IsZero() {
		t.Error("the save has no timestamp")
	}
}

func TestSaveDefaultsToSaveJSON(t *testing.T) {
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("restoring the working directory failed: %v", err)
		}
	})

	g := newTestGame(t, 1)
	if _, err := g.Save(""); err != nil {
		t.Fatalf("Save with no path returned %v", err)
	}
	if _, err := os.Stat(DefaultSavePath); err != nil {
		t.Errorf("Save(\"\") did not write %s: %v", DefaultSavePath, err)
	}
	if _, err := g.Load(""); err != nil {
		t.Errorf("Load with no path returned %v", err)
	}
}
