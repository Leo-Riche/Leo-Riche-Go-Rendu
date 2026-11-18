package repl

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/leoriche/grimhold/internal/ansi"
	"github.com/leoriche/grimhold/internal/game"
)

// register fills the command table. Every command lives here and nowhere
// else: help, the unknown-command message and the alias table are all built
// from this map.
func (r *REPL) register() {
	r.commands = map[string]cliCommand{}
	r.aliases = map[string]string{}

	add := func(c cliCommand) {
		if c.usage == "" {
			c.usage = c.name
		}
		r.commands[c.name] = c
	}

	add(cliCommand{
		name:        "help",
		description: "Show this message",
		callback:    r.cmdHelp,
	})
	add(cliCommand{
		name:        "map",
		description: "List the known regions, their danger and their exits",
		callback:    r.cmdMap,
	})
	add(cliCommand{
		name:        "travel",
		usage:       "travel <region>",
		description: "Travel to a region connected to this one",
		callback:    r.cmdTravel,
	})
	add(cliCommand{
		name:        "look",
		description: "Describe your surroundings",
		callback:    r.cmdLook,
	})
	add(cliCommand{
		name:        "explore",
		description: "Wander the region and risk an encounter",
		callback:    r.cmdExplore,
	})
	add(cliCommand{
		name:        "fight",
		usage:       "fight [all]",
		description: "Trade one round of blows, or fight all to the finish",
		callback:    r.cmdFight,
	})
	add(cliCommand{
		name:        "flee",
		description: "Try to break away from a fight (a failed roll costs you a hit)",
		callback:    r.cmdFlee,
	})
	add(cliCommand{
		name:        "rest",
		description: "Sleep off every wound, once per region visit",
		callback:    r.cmdRest,
	})
	add(cliCommand{
		name:        "character",
		description: "Print your character sheet",
		callback:    r.cmdCharacter,
	})
	add(cliCommand{
		name:        "loot",
		description: "Pick over the region for gold and weapons, once per visit",
		callback:    r.cmdLoot,
	})
	add(cliCommand{
		name:        "shop",
		description: "See what the merchant in this region has on offer",
		callback:    r.cmdShop,
	})
	add(cliCommand{
		name:        "buy",
		usage:       "buy <number|item>",
		description: "Buy from the merchant here, by shop number or by name",
		callback:    r.cmdBuy,
	})
	add(cliCommand{
		name:        "sell",
		usage:       "sell <item>",
		description: "Sell a carried item or weapon for half its worth",
		callback:    r.cmdSell,
	})
	add(cliCommand{
		name:        "inventory",
		description: "List everything you carry and what it sells for",
		callback:    r.cmdInventory,
	})
	add(cliCommand{
		name:        "use",
		usage:       "use <item>",
		description: "Use something from your bag, whatever it is",
		callback:    r.cmdUse,
	})
	add(cliCommand{
		name:        "drink",
		usage:       "drink <potion>",
		description: "Drink a potion (in a fight it costs you your turn)",
		callback:    r.cmdDrink,
	})
	add(cliCommand{
		name:        "read",
		usage:       "read <grimoire>",
		description: "Read a grimoire and learn its spell for good",
		callback:    r.cmdRead,
	})
	add(cliCommand{
		name:        "weapons",
		description: "List what you carry and every melee weapon in the API",
		callback:    r.cmdWeapons,
	})
	add(cliCommand{
		name:        "equip",
		usage:       "equip <weapon>",
		description: "Take a weapon out of your pack",
		callback:    r.cmdEquip,
	})
	add(cliCommand{
		name:        "spells",
		description: "List the spells you chose and whether they are ready",
		callback:    r.cmdSpells,
	})
	add(cliCommand{
		name:        "cast",
		usage:       "cast <spell>",
		description: "Cast one of your spells, once per rest",
		callback:    r.cmdCast,
	})
	add(cliCommand{
		name:        "bestiary",
		usage:       "bestiary [next|prev]",
		description: "Page through the Open5e monster list",
		callback:    r.cmdBestiary,
	})
	add(cliCommand{
		name:        "inspect",
		usage:       "inspect <monster>",
		description: "Print a monster stat block from Open5e",
		callback:    r.cmdInspect,
	})
	add(cliCommand{
		name:        "slain",
		description: "Show your bestiary of kills",
		callback:    r.cmdSlain,
	})
	add(cliCommand{
		name:        "cache",
		description: "Show the state of the Open5e response cache",
		callback:    r.cmdCache,
	})
	add(cliCommand{
		name:        "save",
		usage:       "save [file]",
		description: "Write the game state to save.json",
		callback:    r.cmdSave,
	})
	add(cliCommand{
		name:        "load",
		usage:       "load [file]",
		description: "Restore the game state from save.json",
		callback:    r.cmdLoad,
	})
	add(cliCommand{
		name:        "clear",
		description: "Clear the screen",
		callback:    r.cmdClear,
	})
	add(cliCommand{
		name:        "quit",
		description: "Leave Grimhold (your save is not written for you)",
		callback:    r.cmdQuit,
	})

	for alias, target := range map[string]string{
		"h": "help", "?": "help", "commands": "help",
		"m": "map", "t": "travel", "go": "travel", "l": "look",
		"e": "explore", "f": "fight", "attack": "fight", "r": "rest",
		"c": "character", "char": "character", "sheet": "character",
		"b": "bestiary", "i": "inspect", "kills": "slain",
		"inv": "inventory", "bag": "inventory", "study": "read",
		"store": "shop", "merchant": "shop", "trade": "shop",
		"run": "flee", "exit": "quit", "q": "quit",
	} {
		if _, ok := r.commands[target]; !ok {
			panic("repl: alias " + alias + " points at unknown command " + target)
		}
		r.aliases[alias] = target
	}
}

// noArgs rejects a command that was given arguments it cannot use.
func (r *REPL) noArgs(name string, args []string) error {
	if len(args) == 0 {
		return nil
	}
	return fmt.Errorf("%s takes no arguments (got %d: %s)", name, len(args), strings.Join(args, " "))
}

// joinArgs glues the arguments back together so that multi-word names such as
// "young black dragon" work.
func joinArgs(args []string) string {
	return strings.TrimSpace(strings.Join(args, " "))
}

func (r *REPL) cmdHelp(_ context.Context, args []string) error {
	if err := r.noArgs("help", args); err != nil {
		return err
	}

	names := make([]string, 0, len(r.commands))
	width := 0
	for name, cmd := range r.commands {
		names = append(names, name)
		if n := len(cmd.usage); n > width {
			width = n
		}
	}
	sort.Strings(names)

	r.println(ansi.Bold("Grimhold") + " - a dungeon crawler")
	r.println("")
	r.println("Commands:")
	for _, name := range names {
		cmd := r.commands[name]
		r.printf("  %-*s  %s\n", width, cmd.usage, cmd.description)
	}

	// Aliases are printed grouped by the command they point at.
	byTarget := map[string][]string{}
	for alias, target := range r.aliases {
		byTarget[target] = append(byTarget[target], alias)
	}
	targets := make([]string, 0, len(byTarget))
	for target := range byTarget {
		targets = append(targets, target)
	}
	sort.Strings(targets)

	parts := make([]string, 0, len(targets))
	for _, target := range targets {
		sort.Strings(byTarget[target])
		parts = append(parts, strings.Join(byTarget[target], "/")+"="+target)
	}
	r.println("")
	r.println(ansi.Gray("Shortcuts: " + strings.Join(parts, ", ")))
	return nil
}

func (r *REPL) cmdMap(_ context.Context, args []string) error {
	if err := r.noArgs("map", args); err != nil {
		return err
	}
	r.printLines(r.game.Map())
	return nil
}

func (r *REPL) cmdLook(_ context.Context, args []string) error {
	if err := r.noArgs("look", args); err != nil {
		return err
	}
	r.printLines(r.game.Look())
	return nil
}

func (r *REPL) cmdTravel(ctx context.Context, args []string) error {
	target := joinArgs(args)
	if target == "" {
		return fmt.Errorf("travel where? usage: %s", r.commands["travel"].usage)
	}
	lines, err := r.game.Travel(ctx, target)
	if err != nil {
		return err
	}
	r.printLines(lines)
	return nil
}

func (r *REPL) cmdExplore(ctx context.Context, args []string) error {
	if err := r.noArgs("explore", args); err != nil {
		return err
	}
	lines, err := r.game.Explore(ctx)
	if err != nil {
		return err
	}
	r.printLines(lines)
	return nil
}

func (r *REPL) cmdFight(_ context.Context, args []string) error {
	all := false
	switch {
	case len(args) == 0:
	case len(args) == 1 && strings.EqualFold(args[0], "all"):
		all = true
	default:
		return fmt.Errorf("usage: %s", r.commands["fight"].usage)
	}

	lines, err := r.game.Fight(all)
	r.printLines(lines)
	return err
}

func (r *REPL) cmdFlee(_ context.Context, args []string) error {
	if err := r.noArgs("flee", args); err != nil {
		return err
	}
	lines, err := r.game.RunAway()
	if err != nil {
		return err
	}
	r.printLines(lines)
	return nil
}

func (r *REPL) cmdRest(_ context.Context, args []string) error {
	if err := r.noArgs("rest", args); err != nil {
		return err
	}
	lines, err := r.game.Rest()
	if err != nil {
		return err
	}
	r.printLines(lines)
	return nil
}

func (r *REPL) cmdCharacter(_ context.Context, args []string) error {
	if err := r.noArgs("character", args); err != nil {
		return err
	}
	r.println(r.game.Char.Sheet())
	return nil
}

func (r *REPL) cmdLoot(ctx context.Context, args []string) error {
	if err := r.noArgs("loot", args); err != nil {
		return err
	}
	lines, err := r.game.Loot(ctx)
	if err != nil {
		return err
	}
	r.printLines(lines)
	return nil
}

func (r *REPL) cmdShop(ctx context.Context, args []string) error {
	if err := r.noArgs("shop", args); err != nil {
		return err
	}
	lines, err := r.game.Shop(ctx)
	if err != nil {
		return err
	}
	r.printLines(lines)
	return nil
}

func (r *REPL) cmdBuy(ctx context.Context, args []string) error {
	what := joinArgs(args)
	if what == "" {
		return fmt.Errorf("buy what? usage: %s", r.commands["buy"].usage)
	}
	lines, err := r.game.Buy(ctx, what)
	if err != nil {
		return err
	}
	r.printLines(lines)
	return nil
}

func (r *REPL) cmdSell(_ context.Context, args []string) error {
	what := joinArgs(args)
	if what == "" {
		return fmt.Errorf("sell what? usage: %s", r.commands["sell"].usage)
	}
	lines, err := r.game.Sell(what)
	if err != nil {
		return err
	}
	r.printLines(lines)
	return nil
}

func (r *REPL) cmdInventory(_ context.Context, args []string) error {
	if err := r.noArgs("inventory", args); err != nil {
		return err
	}
	r.printLines(r.game.Inventory())
	return nil
}

func (r *REPL) cmdUse(_ context.Context, args []string) error {
	return r.use("use", "", args)
}

func (r *REPL) cmdDrink(_ context.Context, args []string) error {
	return r.use("drink", game.KindPotion, args)
}

func (r *REPL) cmdRead(_ context.Context, args []string) error {
	return r.use("read", game.KindBook, args)
}

// use is the body of use, drink and read: the only difference is which kind
// of item the name is allowed to match.
func (r *REPL) use(name string, kind game.ItemKind, args []string) error {
	what := joinArgs(args)
	if what == "" {
		return fmt.Errorf("%s what? usage: %s", name, r.commands[name].usage)
	}
	lines, err := r.game.Use(what, kind)
	r.printLines(lines)
	return err
}

func (r *REPL) cmdWeapons(ctx context.Context, args []string) error {
	if err := r.noArgs("weapons", args); err != nil {
		return err
	}
	lines, err := r.game.WeaponList(ctx)
	if err != nil {
		return err
	}
	r.printLines(lines)
	return nil
}

func (r *REPL) cmdEquip(_ context.Context, args []string) error {
	name := joinArgs(args)
	if name == "" {
		return fmt.Errorf("equip what? usage: %s", r.commands["equip"].usage)
	}
	lines, err := r.game.Equip(name)
	if err != nil {
		return err
	}
	r.printLines(lines)
	return nil
}

func (r *REPL) cmdSpells(_ context.Context, args []string) error {
	if err := r.noArgs("spells", args); err != nil {
		return err
	}
	r.printLines(r.game.SpellList())
	return nil
}

func (r *REPL) cmdCast(_ context.Context, args []string) error {
	name := joinArgs(args)
	if name == "" {
		return fmt.Errorf("cast what? usage: %s", r.commands["cast"].usage)
	}
	lines, err := r.game.Cast(name)
	r.printLines(lines)
	return err
}

func (r *REPL) cmdBestiary(ctx context.Context, args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("usage: %s", r.commands["bestiary"].usage)
	}
	direction := ""
	if len(args) == 1 {
		direction = args[0]
	}
	lines, err := r.game.Bestiary(ctx, direction)
	if err != nil {
		return err
	}
	r.printLines(lines)
	return nil
}

func (r *REPL) cmdInspect(ctx context.Context, args []string) error {
	name := joinArgs(args)
	if name == "" {
		return fmt.Errorf("inspect what? usage: %s", r.commands["inspect"].usage)
	}
	lines, err := r.game.Inspect(ctx, name)
	if err != nil {
		return err
	}
	r.printLines(lines)
	return nil
}

func (r *REPL) cmdSlain(_ context.Context, args []string) error {
	if err := r.noArgs("slain", args); err != nil {
		return err
	}
	r.printLines(r.game.SlainList())
	return nil
}

func (r *REPL) cmdCache(_ context.Context, args []string) error {
	if err := r.noArgs("cache", args); err != nil {
		return err
	}
	r.printLines(r.game.CacheStats())
	return nil
}

func (r *REPL) cmdSave(_ context.Context, args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("usage: %s", r.commands["save"].usage)
	}
	path := r.savePath(args)
	lines, err := r.game.Save(path)
	if err != nil {
		return err
	}
	r.printLines(lines)
	return nil
}

func (r *REPL) cmdLoad(_ context.Context, args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("usage: %s", r.commands["load"].usage)
	}
	path := r.savePath(args)
	lines, err := r.game.Load(path)
	if err != nil {
		return err
	}
	r.printLines(lines)
	return nil
}

// savePath is the file a save or load command works on.
func (r *REPL) savePath(args []string) string {
	if len(args) == 1 {
		return args[0]
	}
	if r.opts.SavePath != "" {
		return r.opts.SavePath
	}
	return game.DefaultSavePath
}

func (r *REPL) cmdClear(_ context.Context, args []string) error {
	if err := r.noArgs("clear", args); err != nil {
		return err
	}
	// Clear the screen and park the cursor at the top left.
	r.printf("\x1b[2J\x1b[H")
	return nil
}

func (r *REPL) cmdQuit(_ context.Context, args []string) error {
	if err := r.noArgs("quit", args); err != nil {
		return err
	}
	return errQuit
}
