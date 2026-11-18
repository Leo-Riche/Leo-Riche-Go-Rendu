// Package repl is the read-eval-print loop: it reads a line, parses it into a
// command and arguments, dispatches it through a command table and prints the
// result. Nothing the player can type is allowed to end the loop except quit
// and end of input.
package repl

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/leoriche/grimhold/internal/ansi"
	"github.com/leoriche/grimhold/internal/game"
)

// cliCommand is one entry of the command table. The help text is generated
// from these fields, so a new command is documented by the act of registering
// it and the help output can never drift from reality.
type cliCommand struct {
	name        string
	usage       string
	description string
	callback    func(ctx context.Context, args []string) error
}

// errQuit unwinds the loop when the player asks to leave.
var errQuit = errors.New("quit")

// commandTimeout bounds any single command that talks to the API.
const commandTimeout = 30 * time.Second

// Options configures a REPL.
type Options struct {
	// SavePath is where save and load go when given no argument.
	SavePath string
	// AmbushEvery is the ambush ticker period. Zero disables ambushes.
	AmbushEvery time.Duration
	// AmbushChance is the percent chance that a tick becomes an ambush.
	AmbushChance int
}

// REPL wires the game to an input and an output stream.
type REPL struct {
	game *game.Game
	in   io.Reader
	out  io.Writer
	opts Options

	commands map[string]cliCommand
	aliases  map[string]string
}

// New builds a REPL and registers the command table.
func New(g *game.Game, in io.Reader, out io.Writer, opts Options) *REPL {
	if opts.SavePath == "" {
		opts.SavePath = game.DefaultSavePath
	}
	if opts.AmbushChance <= 0 {
		opts.AmbushChance = 35
	}

	r := &REPL{game: g, in: in, out: out, opts: opts}
	r.register()
	return r
}

func (r *REPL) printf(format string, args ...any) {
	fmt.Fprintf(r.out, format, args...)
}

func (r *REPL) println(s string) {
	fmt.Fprintln(r.out, s)
}

func (r *REPL) printLines(lines []string) {
	for _, line := range lines {
		r.println(line)
	}
}

// Banner is printed once when the game starts.
func (r *REPL) Banner() {
	r.println(ansi.Bold("Grimhold") + " - a dungeon crawler. Monster data from Open5e.")
	r.println(ansi.Gray("Type help for the command list, map to see where you are."))
	r.println("")
	r.printLines(r.game.Look())
	r.println("")
}

// Run reads and dispatches commands until the player quits, the input ends or
// the context is cancelled.
//
// Input is read on its own goroutine and handed over on a channel so that the
// loop can select between a typed line and the ambush ticker: that is what
// lets an ambush interrupt the prompt instead of waiting politely for the
// player to press return.
func (r *REPL) Run(ctx context.Context) error {
	lines := make(chan string)
	readDone := make(chan error, 1)
	go r.readLoop(ctx, lines, readDone)

	ambush := r.newAmbushTicker()
	defer ambush.Stop()

	for {
		r.printf("%s", r.game.Prompt())

		select {
		case <-ctx.Done():
			r.println("")
			r.println("The dark closes over Grimhold.")
			return nil

		case err := <-readDone:
			r.println("")
			if err != nil && !errors.Is(err, io.EOF) {
				return fmt.Errorf("read input: %w", err)
			}
			return nil

		case <-ambush.C:
			r.handleAmbush(ctx)

		case line := <-lines:
			if err := r.dispatch(ctx, line); err != nil {
				if errors.Is(err, errQuit) {
					r.println("You step back out into the rain. Grimhold keeps your name.")
					return nil
				}
				// A command error is a message to the player, never a crash.
				r.println(ansi.Red("! ") + err.Error())
			}
		}
	}
}

// readLoop pushes typed lines onto a channel. A nil error on done means end of
// input.
func (r *REPL) readLoop(ctx context.Context, lines chan<- string, done chan<- error) {
	scanner := bufio.NewScanner(r.in)
	scanner.Buffer(make([]byte, 0, 4096), 1<<20)
	for scanner.Scan() {
		select {
		case lines <- scanner.Text():
		case <-ctx.Done():
			done <- nil
			return
		}
	}
	done <- scanner.Err()
}

// ambushTicker is a time.Ticker that may be switched off, in which case C is a
// channel nothing is ever sent on.
type ambushTicker struct {
	C    <-chan time.Time
	stop func()
}

// Stop releases the ticker.
func (a ambushTicker) Stop() { a.stop() }

// newAmbushTicker starts the goroutine behind the ambush bonus: a time.Ticker
// ticks on its own goroutine and Run selects on it.
func (r *REPL) newAmbushTicker() ambushTicker {
	if r.opts.AmbushEvery <= 0 {
		return ambushTicker{C: make(chan time.Time), stop: func() {}}
	}
	t := time.NewTicker(r.opts.AmbushEvery)
	return ambushTicker{C: t.C, stop: t.Stop}
}

// handleAmbush rolls for an ambush and, if one happens, prints it over the
// prompt. Failures are swallowed: an ambush that cannot reach the API simply
// does not happen.
func (r *REPL) handleAmbush(ctx context.Context) {
	if !r.game.Roller.Chance(r.opts.AmbushChance) {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	lines, err := r.game.Ambush(ctx)
	if err != nil {
		return
	}
	r.printLines(lines)
}

// dispatch parses one line and runs the matching command. It recovers from a
// panic inside a command, so that a bug in one command cannot take the whole
// session down with it.
func (r *REPL) dispatch(ctx context.Context, line string) (err error) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		return nil
	}

	name := strings.ToLower(fields[0])
	if target, ok := r.aliases[name]; ok {
		name = target
	}
	cmd, ok := r.commands[name]
	if !ok {
		return fmt.Errorf("unknown command %q; type help for the list", fields[0])
	}

	defer func() {
		if rec := recover(); rec != nil {
			r.println(ansi.Red(fmt.Sprintf("! something went very wrong in %q: %v", cmd.name, rec)))
			r.println(ansi.Gray("The game is still running."))
			err = nil
		}
	}()

	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	return cmd.callback(ctx, fields[1:])
}

// Dispatch runs a single command line, for tests and for scripted play.
func (r *REPL) Dispatch(ctx context.Context, line string) error {
	err := r.dispatch(ctx, line)
	if errors.Is(err, errQuit) {
		return nil
	}
	return err
}
