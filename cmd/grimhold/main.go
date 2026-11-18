// Command grimhold runs the game: it parses the flags, loads the world, wires
// the Open5e client to the game state and hands everything to the REPL.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/leoriche/grimhold/internal/ansi"
	"github.com/leoriche/grimhold/internal/dice"
	"github.com/leoriche/grimhold/internal/game"
	"github.com/leoriche/grimhold/internal/open5e"
	"github.com/leoriche/grimhold/internal/repl"
	"github.com/leoriche/grimhold/internal/world"
)

// config is the parsed command line.
type config struct {
	worldPath   string
	savePath    string
	resume      bool
	name        string
	noColor     bool
	noSpells    bool
	seed        int64
	ambushEvery time.Duration
	cacheTTL    time.Duration
	reapEvery   time.Duration
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "grimhold: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	cfg := parseFlags()
	if cfg.noColor {
		// Colour is on unless NO_COLOR or TERM=dumb already turned it off, so
		// the flag only ever disables it.
		ansi.Enable(false)
	}

	w, err := world.Load(world.DefaultPath(cfg.worldPath))
	if err != nil {
		return fmt.Errorf("%w\n(pass -world <path> to point at a world file)", err)
	}

	client := open5e.NewClient(
		open5e.WithCache(open5e.NewCache(cfg.cacheTTL, cfg.reapEvery)),
	)

	roller := dice.NewRandom()
	if cfg.seed != 0 {
		roller = dice.New(cfg.seed)
	}

	// Signals end the session cleanly: the cache reaper is stopped and any
	// background prefetch is waited for.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	stdin := bufio.NewReader(os.Stdin)
	out := os.Stdout

	g := game.New(w, client, roller, game.NewCharacter(cfg.name))
	defer g.Close()

	resumed := false
	if cfg.resume {
		lines, err := g.Load(cfg.savePath)
		switch {
		case err == nil:
			resumed = true
			printLines(out, lines)
		case errors.Is(err, game.ErrNoSave):
			fmt.Fprintln(out, ansi.Gray("No save file yet; starting a new game."))
		default:
			return err
		}
	}

	if !resumed {
		if err := createCharacter(ctx, cfg, g, stdin, out); err != nil {
			return err
		}
	}

	r := repl.New(g, stdin, out, repl.Options{
		SavePath:    cfg.savePath,
		AmbushEvery: cfg.ambushEvery,
	})
	r.Banner()
	return r.Run(ctx)
}

func parseFlags() config {
	var cfg config
	flag.StringVar(&cfg.worldPath, "world", "", "path to world.json (default: ./world.json, then next to the binary)")
	flag.StringVar(&cfg.savePath, "save", game.DefaultSavePath, "save file used by the save and load commands")
	flag.BoolVar(&cfg.resume, "continue", false, "load the save file before starting")
	flag.StringVar(&cfg.name, "name", "", "character name (skips the question at startup)")
	flag.BoolVar(&cfg.noColor, "no-color", false, "disable ANSI colour")
	flag.BoolVar(&cfg.noSpells, "no-spells", false, "start without choosing spells")
	flag.Int64Var(&cfg.seed, "seed", 0, "fix the dice seed, for a reproducible session")
	flag.DurationVar(&cfg.ambushEvery, "ambush", 25*time.Second, "how often an ambush may interrupt the prompt (0 disables)")
	flag.DurationVar(&cfg.cacheTTL, "cache-ttl", open5e.DefaultTTL, "how long an Open5e response stays fresh")
	flag.DurationVar(&cfg.reapEvery, "cache-reap", open5e.DefaultReapInterval, "how often the cache reaper purges expired entries")

	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "Grimhold - a terminal dungeon crawler fed by the Open5e API.")
		fmt.Fprintln(flag.CommandLine.Output(), "\nUsage: grimhold [flags]\n\nFlags:")
		flag.PrintDefaults()
	}
	flag.Parse()
	return cfg
}

// createCharacter asks for a name and, unless -no-spells was passed, offers
// two spells pulled from the API.
//
// When standard input is not a terminal (a piped script, or a test) the
// questions are skipped and the defaults are taken, so that a scripted session
// is not thrown off by prompts.
func createCharacter(ctx context.Context, cfg config, g *game.Game, in *bufio.Reader, out io.Writer) error {
	interactive := isTerminal(os.Stdin)

	name := strings.TrimSpace(cfg.name)
	if name == "" && interactive {
		fmt.Fprint(out, "What name will they carve into the wall? ")
		line, err := in.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read name: %w", err)
		}
		name = strings.TrimSpace(line)
	}
	g.Char = game.NewCharacter(name)

	if cfg.noSpells {
		return nil
	}

	fetchCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	offers := game.OfferSpells(fetchCtx, g.Client)
	if len(offers) == 0 {
		fmt.Fprintln(out, ansi.Gray("The arcane tomes are unreadable tonight (Open5e unreachable); you begin with steel only."))
		return nil
	}

	if !interactive {
		// Scripted session: take the first two on offer.
		g.Char.Spells = offers[:min(game.SpellsPerCharacter, len(offers))]
		return nil
	}

	fmt.Fprintln(out, "")
	fmt.Fprintln(out, ansi.Bold("Two spells have stuck in your memory. Which two?"))
	for i, s := range offers {
		fmt.Fprintf(out, "  %d) %s\n", i+1, s.Summary())
	}
	fmt.Fprintf(out, "Pick %d numbers (for example \"1 8\"), or press return for none: ", game.SpellsPerCharacter)

	line, err := in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read spell choice: %w", err)
	}

	picked := pickSpells(offers, strings.Fields(line))
	g.Char.Spells = picked
	switch len(picked) {
	case 0:
		fmt.Fprintln(out, ansi.Gray("You take no spells. Steel it is."))
	default:
		names := make([]string, 0, len(picked))
		for _, s := range picked {
			names = append(names, s.Name)
		}
		fmt.Fprintf(out, "You carry %s.\n", strings.Join(names, " and "))
	}
	fmt.Fprintln(out, "")
	return nil
}

// pickSpells maps the numbers the player typed onto the offered spells,
// ignoring anything that is not a number in range and stopping once enough
// have been chosen.
func pickSpells(offers []game.Spell, fields []string) []game.Spell {
	var picked []game.Spell
	seen := map[int]bool{}
	for _, field := range fields {
		if len(picked) >= game.SpellsPerCharacter {
			break
		}
		n, err := strconv.Atoi(strings.Trim(field, ",.;"))
		if err != nil || n < 1 || n > len(offers) || seen[n] {
			continue
		}
		seen[n] = true
		picked = append(picked, offers[n-1])
	}
	return picked
}

// isTerminal reports whether f is a character device, which is the standard
// library way of telling a terminal from a pipe or a file.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func printLines(w io.Writer, lines []string) {
	for _, line := range lines {
		fmt.Fprintln(w, line)
	}
}
