package repl

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leoriche/grimhold/internal/ansi"
	"github.com/leoriche/grimhold/internal/dice"
	"github.com/leoriche/grimhold/internal/game"
	"github.com/leoriche/grimhold/internal/open5e"
	"github.com/leoriche/grimhold/internal/world"
)

// The REPL tests never touch the network either: the client below is aimed at
// a local server serving two stat blocks.

const testWorldJSON = `{
  "start": "crossroads",
  "regions": [
    {"id": "crossroads", "name": "The Crossroads", "description": "Four roads.",
     "danger": 0, "foes": ["goblin"], "exits": ["marsh"]},
    {"id": "marsh", "name": "Ashen Marsh", "description": "Fog and iron.",
     "danger": 1, "foes": ["goblin"], "exits": ["crossroads"]}
  ]
}`

const testGoblinJSON = `{
  "slug": "goblin", "name": "Goblin", "size": "Small", "type": "Humanoid",
  "armor_class": 15, "hit_points": 7, "challenge_rating": "1/4", "cr": 0.25,
  "speed": {"walk": 30},
  "actions": [{"name": "Scimitar", "attack_bonus": 4, "damage_dice": "1d6", "damage_bonus": 2}]
}`

const testWeaponsJSON = `{"count":1,"next":null,"previous":null,"results":[
  {"name":"Greatsword","slug":"greatsword","category":"Martial Melee Weapons",
   "cost":"50 gp","damage_dice":"2d6","damage_type":"slashing","document__slug":"wotc-srd"}]}`

func init() {
	// Keep the assertions below free of escape codes.
	ansi.Enable(false)
}

// newTestREPL returns a REPL reading from input, its output buffer and the
// game behind it.
func newTestREPL(t *testing.T, input string) (*REPL, *bytes.Buffer, *game.Game) {
	t.Helper()

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	mux.HandleFunc("/v1/weapons/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, testWeaponsJSON)
	})
	mux.HandleFunc("/v1/monsters/goblin/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, testGoblinJSON)
	})
	mux.HandleFunc("/v1/monsters/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/monsters/" {
			http.Error(w, `{"detail":"Not found."}`, http.StatusNotFound)
			return
		}
		fmt.Fprintf(w, `{"count":1,"next":null,"previous":null,"results":[%s]}`, testGoblinJSON)
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

	g := game.New(w, client, dice.New(1), game.NewCharacter("Tester"))
	t.Cleanup(g.Close)

	out := &bytes.Buffer{}
	r := New(g, strings.NewReader(input), out, Options{
		SavePath: filepath.Join(t.TempDir(), "save.json"),
	})
	return r, out, g
}

func TestHelpIsGeneratedFromTheCommandTable(t *testing.T) {
	r, out, _ := newTestREPL(t, "")

	if err := r.Dispatch(context.Background(), "help"); err != nil {
		t.Fatalf("help returned %v", err)
	}

	got := out.String()
	for name, cmd := range r.commands {
		if !strings.Contains(got, cmd.usage) {
			t.Errorf("help does not list %q (usage %q)", name, cmd.usage)
		}
		if !strings.Contains(got, cmd.description) {
			t.Errorf("help does not describe %q", name)
		}
	}
	for _, want := range []string{"Commands:", "Shortcuts:"} {
		if !strings.Contains(got, want) {
			t.Errorf("help output is missing %q", want)
		}
	}
}

func TestEveryAliasPointsAtARegisteredCommand(t *testing.T) {
	r, _, _ := newTestREPL(t, "")

	for alias, target := range r.aliases {
		if _, ok := r.commands[target]; !ok {
			t.Errorf("alias %q points at unknown command %q", alias, target)
		}
		if _, clash := r.commands[alias]; clash {
			t.Errorf("alias %q shadows a real command", alias)
		}
	}
}

func TestEmptyInputIsIgnored(t *testing.T) {
	r, out, _ := newTestREPL(t, "")

	for _, line := range []string{"", "   ", "\t", "  \t  "} {
		if err := r.Dispatch(context.Background(), line); err != nil {
			t.Errorf("dispatching %q returned %v", line, err)
		}
	}
	if out.Len() != 0 {
		t.Errorf("empty input produced output: %q", out.String())
	}
}

func TestUnknownCommand(t *testing.T) {
	r, _, _ := newTestREPL(t, "")

	err := r.Dispatch(context.Background(), "frobnicate the goblin")
	if err == nil {
		t.Fatal("an unknown command must return an error")
	}
	if !strings.Contains(err.Error(), "frobnicate") || !strings.Contains(err.Error(), "help") {
		t.Errorf("error = %q, want it to name the command and point at help", err)
	}
}

func TestAliasesAndCaseAreAccepted(t *testing.T) {
	for _, line := range []string{"look", "LOOK", "l", "  Look  "} {
		r, out, _ := newTestREPL(t, "")
		if err := r.Dispatch(context.Background(), line); err != nil {
			t.Fatalf("%q returned %v", line, err)
		}
		if !strings.Contains(out.String(), "Four roads") {
			t.Errorf("%q did not run the look command: %q", line, out.String())
		}
	}
}

func TestWrongArgumentCountsAreRefusedNotCrashed(t *testing.T) {
	lines := []string{
		"map extra",
		"look here",
		"help me",
		"travel",
		"travel nowhere",
		"explore quickly",
		"fight sideways",
		"flee fast",
		"rest a while",
		"character sheet please",
		"inspect",
		"equip",
		"cast",
		"bestiary one two",
		"save a b",
		"load a b",
		"quit now",
		"spells all",
		"cache please",
		"loot everything",
		"weapons all",
		"slain list",
		"clear screen",
	}

	for _, line := range lines {
		r, _, _ := newTestREPL(t, "")
		err := r.Dispatch(context.Background(), line)
		if err == nil {
			t.Errorf("%q should have been refused", line)
		}
	}
}

func TestDispatchRecoversFromAPanickingCommand(t *testing.T) {
	r, out, _ := newTestREPL(t, "")
	r.commands["boom"] = cliCommand{
		name:        "boom",
		usage:       "boom",
		description: "a command with a bug in it",
		callback: func(context.Context, []string) error {
			panic("the goblin ate the pointer")
		},
	}

	if err := r.Dispatch(context.Background(), "boom"); err != nil {
		t.Errorf("a recovered panic should not surface as an error, got %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "went very wrong") || !strings.Contains(got, "still running") {
		t.Errorf("the recovery message is missing: %q", got)
	}

	// The REPL is still usable afterwards.
	out.Reset()
	if err := r.Dispatch(context.Background(), "look"); err != nil {
		t.Fatalf("the REPL stopped working after a panic: %v", err)
	}
	if !strings.Contains(out.String(), "Four roads") {
		t.Error("look stopped working after a recovered panic")
	}
}

func TestRunStopsAtEndOfInput(t *testing.T) {
	r, out, _ := newTestREPL(t, "look\nmap\n")

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "The Crossroads") || !strings.Contains(got, "Known regions") {
		t.Errorf("both commands should have run:\n%s", got)
	}
	if strings.Count(got, "grimhold") < 2 {
		t.Error("the prompt should be printed before every read")
	}
}

func TestRunQuitsOnQuit(t *testing.T) {
	r, out, _ := newTestREPL(t, "quit\nlook\n")

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "keeps your name") {
		t.Errorf("the goodbye line is missing:\n%s", got)
	}
	// The prompt names the region, so look is identified by its description.
	if strings.Contains(got, "Four roads") {
		t.Error("the look after quit should never have run")
	}
}

func TestRunSurvivesAFloodOfNonsense(t *testing.T) {
	nonsense := strings.Join([]string{
		"", "   ", "???", "travel", "travel ../../etc/passwd",
		"inspect", "fight", "flee", "cast", "equip", "load /nope/nope.json",
		strings.Repeat("a", 5000), "map extra", "help me", "quit",
	}, "\n") + "\n"

	r, out, g := newTestREPL(t, nonsense)
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if g.Location != "crossroads" {
		t.Errorf("nonsense input moved the player to %q", g.Location)
	}
	if !strings.Contains(out.String(), "keeps your name") {
		t.Error("the loop did not reach the quit at the end")
	}
}

func TestRunStopsWhenTheContextIsCancelled(t *testing.T) {
	// A reader that never returns, standing in for a player who types nothing.
	r, out, _ := newTestREPL(t, "")
	r.in = blockingReader{}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run ignored the cancelled context")
	}
	if !strings.Contains(out.String(), "The dark closes over") {
		t.Errorf("the shutdown line is missing: %q", out.String())
	}
}

// blockingReader blocks for a long time instead of returning data or EOF.
type blockingReader struct{}

func (blockingReader) Read([]byte) (int, error) {
	time.Sleep(30 * time.Second)
	return 0, nil
}

func TestScriptedSessionPlaysThrough(t *testing.T) {
	savePath := filepath.Join(t.TempDir(), "save.json")
	script := strings.Join([]string{
		"travel marsh",
		"look",
		"explore", "explore", "explore", "explore", "explore",
		"fight all",
		"character",
		"slain",
		"rest",
		"loot",
		"weapons",
		"inspect goblin",
		"bestiary",
		"cache",
		"save " + savePath,
		"load " + savePath,
		"quit",
	}, "\n") + "\n"

	r, out, g := newTestREPL(t, script)
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run returned %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"Ashen Marsh",
		"Goblin",
		"Armor Class  15",
		"Greatsword",
		"Open5e response cache",
		"Saved to",
		"Loaded",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the session output never mentions %q", want)
		}
	}
	if g.Char.XP == 0 {
		t.Error("five explores and a fight to the finish earned no experience")
	}
	if !game.SaveExists(savePath) {
		t.Error("the save command wrote no file")
	}
}

func TestAmbushNeedsADangerousRegion(t *testing.T) {
	r, out, g := newTestREPL(t, "")
	r.opts.AmbushChance = 100

	// The crossroads are quiet: an ambush there must silently do nothing.
	r.handleAmbush(context.Background())
	if g.Foe != nil {
		t.Errorf("an ambush happened in a danger 0 region: %+v", g.Foe)
	}
	if out.Len() != 0 {
		t.Errorf("a refused ambush printed %q", out.String())
	}

	if _, err := g.Travel(context.Background(), "marsh"); err != nil {
		t.Fatalf("travel failed: %v", err)
	}
	r.handleAmbush(context.Background())
	if g.Foe == nil {
		t.Fatal("an ambush with a 100% chance in a danger 1 region did not happen")
	}
	if !strings.Contains(out.String(), "AMBUSH") {
		t.Errorf("the ambush was not announced: %q", out.String())
	}
}

func TestAmbushNeverHappensAtZeroChance(t *testing.T) {
	r, _, g := newTestREPL(t, "")
	r.opts.AmbushChance = 0

	if _, err := g.Travel(context.Background(), "marsh"); err != nil {
		t.Fatalf("travel failed: %v", err)
	}
	for i := 0; i < 50; i++ {
		r.handleAmbush(context.Background())
	}
	if g.Foe != nil {
		t.Error("an ambush happened although the chance is zero")
	}
}

func TestAmbushTickerCanBeDisabled(t *testing.T) {
	r, _, _ := newTestREPL(t, "")

	r.opts.AmbushEvery = 0
	off := r.newAmbushTicker()
	defer off.Stop()
	select {
	case <-off.C:
		t.Error("a disabled ambush ticker must never tick")
	case <-time.After(50 * time.Millisecond):
	}

	r.opts.AmbushEvery = time.Millisecond
	on := r.newAmbushTicker()
	defer on.Stop()
	select {
	case <-on.C:
	case <-time.After(2 * time.Second):
		t.Error("the ambush ticker never ticked")
	}
}

// TestAmbushInterruptsThePromptDuringRun is the bonus feature end to end: the
// loop is waiting on input and the ambush arrives anyway.
func TestAmbushInterruptsThePromptDuringRun(t *testing.T) {
	r, out, g := newTestREPL(t, "")
	if _, err := g.Travel(context.Background(), "marsh"); err != nil {
		t.Fatalf("travel failed: %v", err)
	}

	r.in = blockingReader{}
	r.opts.AmbushEvery = 5 * time.Millisecond
	r.opts.AmbushChance = 100

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), "AMBUSH") {
			cancel()
			<-done
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	t.Fatalf("no ambush interrupted the prompt:\n%s", out.String())
}

func TestSavePathDefaultsToTheOption(t *testing.T) {
	r, _, _ := newTestREPL(t, "")

	if got := r.savePath(nil); got != r.opts.SavePath {
		t.Errorf("savePath(nil) = %q, want the configured %q", got, r.opts.SavePath)
	}
	if got := r.savePath([]string{"other.json"}); got != "other.json" {
		t.Errorf("savePath(other.json) = %q", got)
	}

	r.opts.SavePath = ""
	if got := r.savePath(nil); got != game.DefaultSavePath {
		t.Errorf("savePath with no option = %q, want %q", got, game.DefaultSavePath)
	}
}

func TestBannerShowsWhereYouAre(t *testing.T) {
	r, out, _ := newTestREPL(t, "")
	r.Banner()

	got := out.String()
	for _, want := range []string{"Grimhold", "help", "The Crossroads"} {
		if !strings.Contains(got, want) {
			t.Errorf("the banner is missing %q:\n%s", want, got)
		}
	}
}
