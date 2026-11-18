package ansi

import (
	"strings"
	"testing"
)

func TestEnableTogglesEscapeCodes(t *testing.T) {
	t.Cleanup(func() { Enable(true) })

	Enable(false)
	if Enabled() {
		t.Fatal("Enabled() is true after Enable(false)")
	}
	if got := Red("danger"); got != "danger" {
		t.Errorf("with colour off, Red = %q, want the plain string", got)
	}

	Enable(true)
	got := Red("danger")
	if !strings.HasPrefix(got, "\x1b[31m") || !strings.HasSuffix(got, "\x1b[0m") {
		t.Errorf("Red = %q, want it wrapped in escape codes", got)
	}
	if Strip(got) != "danger" {
		t.Errorf("Strip(%q) = %q, want danger", got, Strip(got))
	}
}

func TestEmptyStringIsNeverWrapped(t *testing.T) {
	t.Cleanup(func() { Enable(true) })
	Enable(true)

	if got := Bold(""); got != "" {
		t.Errorf("Bold(\"\") = %q, want an empty string", got)
	}
}

func TestBarClampsAndFills(t *testing.T) {
	t.Cleanup(func() { Enable(true) })
	Enable(false)

	tests := []struct {
		name          string
		cur, max, wid int
		want          string
	}{
		{name: "full", cur: 10, max: 10, wid: 10, want: "[##########]"},
		{name: "half", cur: 5, max: 10, wid: 10, want: "[#####.....]"},
		{name: "empty", cur: 0, max: 10, wid: 10, want: "[..........]"},
		{name: "one point always shows", cur: 1, max: 100, wid: 10, want: "[#.........]"},
		{name: "over the maximum is clamped", cur: 50, max: 10, wid: 5, want: "[#####]"},
		{name: "negative is clamped", cur: -5, max: 10, wid: 5, want: "[.....]"},
		{name: "zero maximum does not divide by zero", cur: 0, max: 0, wid: 4, want: "[....]"},
		{name: "zero width still draws something", cur: 1, max: 1, wid: 0, want: "[#]"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Bar(tc.cur, tc.max, tc.wid); got != tc.want {
				t.Errorf("Bar(%d, %d, %d) = %q, want %q", tc.cur, tc.max, tc.wid, got, tc.want)
			}
		})
	}
}

func TestHPShowsTheNumbers(t *testing.T) {
	t.Cleanup(func() { Enable(true) })
	Enable(false)

	if got := HP(7, 30, 10); got != "[##........] 7/30" {
		t.Errorf("HP(7, 30, 10) = %q", got)
	}
}

func TestBarColourFollowsTheRatio(t *testing.T) {
	t.Cleanup(func() { Enable(true) })
	Enable(true)

	tests := []struct {
		cur, max int
		code     string
	}{
		{cur: 10, max: 10, code: green},
		{cur: 5, max: 10, code: yellow},
		{cur: 1, max: 10, code: red},
	}
	for _, tc := range tests {
		if got := Bar(tc.cur, tc.max, 10); !strings.Contains(got, tc.code) {
			t.Errorf("Bar(%d/%d) = %q, want the colour %q", tc.cur, tc.max, got, tc.code)
		}
	}
}

func TestDangerPips(t *testing.T) {
	t.Cleanup(func() { Enable(true) })
	Enable(false)

	tests := map[int]string{
		0:  "-----",
		1:  "*----",
		3:  "***--",
		5:  "*****",
		9:  "*****",
		-3: "-----",
	}
	for level, want := range tests {
		if got := Danger(level); got != want {
			t.Errorf("Danger(%d) = %q, want %q", level, got, want)
		}
	}
}

func TestStripLeavesPlainTextAlone(t *testing.T) {
	if got := Strip("no escapes here"); got != "no escapes here" {
		t.Errorf("Strip = %q", got)
	}
	if got := Strip("\x1b[1mbold\x1b[0m and \x1b[31mred\x1b[0m"); got != "bold and red" {
		t.Errorf("Strip = %q", got)
	}
	// An unterminated escape must not run off the end of the string.
	if got := Strip("text\x1b[3"); got != "text" {
		t.Errorf("Strip of a truncated escape = %q, want \"text\"", got)
	}
}
