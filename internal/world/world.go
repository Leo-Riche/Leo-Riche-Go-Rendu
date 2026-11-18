// Package world loads and validates the Grimhold map: a JSON file describing
// regions, how dangerous they are, what lives in them and how they connect.
package world

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrUnknownRegion is returned for a region id the map does not contain.
var ErrUnknownRegion = errors.New("unknown region")

// Region is one place the player can stand in.
type Region struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Danger      int      `json:"danger"`
	Foes        []string `json:"foes"`
	Exits       []string `json:"exits"`

	// MinLevel gates a region behind a character level. Zero means no gate.
	MinLevel int `json:"min_level,omitempty"`
	// Boss names an Open5e monster slug that is fought once, instead of a
	// random foe, the first time the player explores here.
	Boss string `json:"boss,omitempty"`
}

// World is the whole map plus a lookup index.
type World struct {
	Start   string   `json:"start"`
	Regions []Region `json:"regions"`

	index map[string]*Region
}

// Load reads and validates a world file.
func Load(path string) (*World, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load world %q: %w", path, err)
	}
	w, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("load world %q: %w", path, err)
	}
	return w, nil
}

// Parse decodes a world from JSON and validates it.
func Parse(data []byte) (*World, error) {
	var w World
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil {
		return nil, fmt.Errorf("decode world: %w", err)
	}
	if err := w.init(); err != nil {
		return nil, err
	}
	return &w, nil
}

func (w *World) init() error {
	if len(w.Regions) == 0 {
		return errors.New("world has no regions")
	}
	w.index = make(map[string]*Region, len(w.Regions))
	for i := range w.Regions {
		r := &w.Regions[i]
		switch {
		case r.ID == "":
			return fmt.Errorf("region %d has no id", i)
		case r.Name == "":
			return fmt.Errorf("region %q has no name", r.ID)
		case len(r.Foes) == 0:
			return fmt.Errorf("region %q has no foes", r.ID)
		case r.Danger < 0:
			return fmt.Errorf("region %q has a negative danger", r.ID)
		}
		if _, dup := w.index[r.ID]; dup {
			return fmt.Errorf("duplicate region id %q", r.ID)
		}
		w.index[r.ID] = r
	}
	if w.Start == "" {
		return errors.New("world has no start region")
	}
	if _, ok := w.index[w.Start]; !ok {
		return fmt.Errorf("start region %q does not exist", w.Start)
	}
	for _, r := range w.Regions {
		for _, exit := range r.Exits {
			if _, ok := w.index[exit]; !ok {
				return fmt.Errorf("region %q has an exit to unknown region %q", r.ID, exit)
			}
			if exit == r.ID {
				return fmt.Errorf("region %q has an exit to itself", r.ID)
			}
		}
	}
	return nil
}

// Region returns a region by id.
func (w *World) Region(id string) (*Region, error) {
	r, ok := w.index[strings.ToLower(strings.TrimSpace(id))]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownRegion, id)
	}
	return r, nil
}

// Has reports whether the world contains a region id.
func (w *World) Has(id string) bool {
	_, ok := w.index[strings.ToLower(strings.TrimSpace(id))]
	return ok
}

// Sorted returns the regions ordered by danger then name, which is the order
// the "map" command prints them in.
func (w *World) Sorted() []Region {
	out := make([]Region, len(w.Regions))
	copy(out, w.Regions)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Danger != out[j].Danger {
			return out[i].Danger < out[j].Danger
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Connects reports whether the region has an exit leading to id.
func (r *Region) Connects(id string) bool {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, exit := range r.Exits {
		if exit == id {
			return true
		}
	}
	return false
}

// DangerLabel describes the danger rating in words.
func (r *Region) DangerLabel() string {
	switch {
	case r.Danger <= 0:
		return "quiet"
	case r.Danger == 1:
		return "uneasy"
	case r.Danger == 2:
		return "dangerous"
	case r.Danger == 3:
		return "deadly"
	case r.Danger == 4:
		return "murderous"
	default:
		return "hopeless"
	}
}

// Rolls is how many times explore() draws from the foe list before keeping the
// worst result. Deeper regions roll more often.
func (r *Region) Rolls() int {
	return 1 + r.Danger/2
}

// DefaultPath finds world.json: the -world flag if given, then the working
// directory, then the directory holding the executable. The returned path may
// not exist, in which case the caller reports a readable error.
func DefaultPath(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	const name = "world.json"
	if _, err := os.Stat(name); err == nil {
		return name
	}
	if exe, err := os.Executable(); err == nil {
		beside := filepath.Join(filepath.Dir(exe), name)
		if _, err := os.Stat(beside); err == nil {
			return beside
		}
	}
	return name
}
