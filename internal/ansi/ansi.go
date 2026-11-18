// Package ansi holds the hand-rolled escape codes Grimhold uses for colour,
// HP bars and danger ratings. Colour is off when the NO_COLOR environment
// variable is set, when TERM is "dumb", or when the player passes -no-color.
package ansi

import (
	"fmt"
	"os"
	"strings"
)

// Escape sequences. See https://en.wikipedia.org/wiki/ANSI_escape_code.
const (
	reset   = "\x1b[0m"
	bold    = "\x1b[1m"
	dim     = "\x1b[2m"
	red     = "\x1b[31m"
	green   = "\x1b[32m"
	yellow  = "\x1b[33m"
	blue    = "\x1b[34m"
	magenta = "\x1b[35m"
	cyan    = "\x1b[36m"
	gray    = "\x1b[90m"
)

var enabled = defaultEnabled()

func defaultEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	return true
}

// Enable turns colour on or off for the whole program.
func Enable(on bool) { enabled = on }

// Enabled reports whether colour output is currently on.
func Enabled() bool { return enabled }

func wrap(code, s string) string {
	if !enabled || s == "" {
		return s
	}
	return code + s + reset
}

// Bold renders s in bold.
func Bold(s string) string { return wrap(bold, s) }

// Dim renders s dimmed.
func Dim(s string) string { return wrap(dim, s) }

// Red renders s in red.
func Red(s string) string { return wrap(red, s) }

// Green renders s in green.
func Green(s string) string { return wrap(green, s) }

// Yellow renders s in yellow.
func Yellow(s string) string { return wrap(yellow, s) }

// Blue renders s in blue.
func Blue(s string) string { return wrap(blue, s) }

// Magenta renders s in magenta.
func Magenta(s string) string { return wrap(magenta, s) }

// Cyan renders s in cyan.
func Cyan(s string) string { return wrap(cyan, s) }

// Gray renders s in bright black.
func Gray(s string) string { return wrap(gray, s) }

// Bar draws a fixed-width meter, green above two thirds, yellow above a
// third, red below. cur is clamped into [0,max].
func Bar(cur, max, width int) string {
	if width < 1 {
		width = 1
	}
	if max < 1 {
		max = 1
	}
	if cur < 0 {
		cur = 0
	}
	if cur > max {
		cur = max
	}
	filled := cur * width / max
	if filled == 0 && cur > 0 {
		filled = 1
	}
	bar := strings.Repeat("#", filled) + strings.Repeat(".", width-filled)
	ratio := float64(cur) / float64(max)
	switch {
	case ratio > 2.0/3.0:
		bar = Green(bar)
	case ratio > 1.0/3.0:
		bar = Yellow(bar)
	default:
		bar = Red(bar)
	}
	return "[" + bar + "]"
}

// HP renders an HP bar followed by the numbers, e.g. "[####......] 12/30".
func HP(cur, max, width int) string {
	return fmt.Sprintf("%s %d/%d", Bar(cur, max, width), cur, max)
}

// Danger renders a region's danger rating as coloured pips.
func Danger(level int) string {
	if level < 0 {
		level = 0
	}
	if level > 5 {
		level = 5
	}
	pips := strings.Repeat("*", level) + strings.Repeat("-", 5-level)
	switch {
	case level == 0:
		return Green(pips)
	case level <= 2:
		return Yellow(pips)
	default:
		return Red(pips)
	}
}

// Strip removes every escape sequence this package can emit. Tests use it to
// compare output without caring about colour.
func Strip(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\x1b' {
			b.WriteByte(s[i])
			continue
		}
		for i < len(s) && s[i] != 'm' {
			i++
		}
	}
	return b.String()
}
