package game

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/leoriche/grimhold/internal/ansi"
)

// DefaultSavePath is where save and load look when given no argument.
const DefaultSavePath = "save.json"

// saveVersion lets a future format reject an older file instead of
// misreading it.
const saveVersion = 1

// ErrNoSave means the save file does not exist, which is not a crash.
var ErrNoSave = errors.New("no save file")

// SaveFile is the on-disk game state. A pending encounter is stored too, so
// quitting mid-fight and coming back puts you back in front of the same foe.
type SaveFile struct {
	Version     int              `json:"version"`
	SavedAt     time.Time        `json:"saved_at"`
	Location    string           `json:"location"`
	Rested      bool             `json:"rested"`
	Looted      bool             `json:"looted"`
	Character   *Character       `json:"character"`
	Slain       map[string]*Kill `json:"slain"`
	BossesSlain map[string]bool  `json:"bosses_slain,omitempty"`
	Foe         *Foe             `json:"foe,omitempty"`
}

// Save writes the whole game state as JSON.
//
// The write is atomic: the bytes go to a temporary file in the same directory
// and are only renamed over the real save once they are safely on disk. A
// crash, a quit or a death part way through therefore leaves the previous save
// intact rather than a half-written file.
func (g *Game) Save(path string) ([]string, error) {
	if path == "" {
		path = DefaultSavePath
	}

	snapshot := SaveFile{
		Version:     saveVersion,
		SavedAt:     time.Now().UTC(),
		Location:    g.Location,
		Rested:      g.Rested,
		Looted:      g.Looted,
		Character:   g.Char,
		Slain:       g.Slain,
		BossesSlain: g.BossesSlain,
		Foe:         g.Foe,
	}

	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("save %q: %w", path, err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".grimhold-save-*")
	if err != nil {
		return nil, fmt.Errorf("save %q: %w", path, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename below succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("save %q: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("save %q: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("save %q: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return nil, fmt.Errorf("save %q: %w", path, err)
	}

	return []string{fmt.Sprintf("Saved to %s (%d bytes).", ansi.Cyan(path), len(data))}, nil
}

// Load restores a save file over the current game. The file is fully decoded
// and validated before any game state is touched, so a corrupt or hand-edited
// save leaves the running session exactly as it was.
func (g *Game) Load(path string) ([]string, error) {
	if path == "" {
		path = DefaultSavePath
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w at %s; play a while and use save", ErrNoSave, path)
		}
		return nil, fmt.Errorf("load %q: %w", path, err)
	}

	var snapshot SaveFile
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, fmt.Errorf("load %q: the file is not a Grimhold save: %w", path, err)
	}
	if err := g.validateSave(&snapshot); err != nil {
		return nil, fmt.Errorf("load %q: %w", path, err)
	}

	// Past this point nothing can fail, so the swap is all or nothing.
	g.Char = snapshot.Character
	g.Location = snapshot.Location
	g.Rested = snapshot.Rested
	g.Looted = snapshot.Looted
	g.Slain = snapshot.Slain
	g.BossesSlain = snapshot.BossesSlain
	g.Foe = snapshot.Foe
	g.RoundNumber = 0

	if g.Slain == nil {
		g.Slain = map[string]*Kill{}
	}
	if g.BossesSlain == nil {
		g.BossesSlain = map[string]bool{}
	}
	if g.Char.SpellsUsed == nil {
		g.Char.SpellsUsed = map[string]bool{}
	}

	lines := []string{
		fmt.Sprintf("Loaded %s, saved %s.", ansi.Cyan(path), snapshot.SavedAt.Local().Format("2006-01-02 15:04")),
		fmt.Sprintf("%s, level %d, %s, in %s.",
			ansi.Bold(g.Char.Name), g.Char.Level, ansi.HP(g.Char.HP, g.Char.MaxHP, 16), g.Region().Name),
	}
	if g.Foe != nil {
		lines = append(lines, ansi.Red("You left a fight unfinished:"), "  "+g.Foe.Describe())
	}
	return lines, nil
}

// validateSave checks a decoded save against the running world and rebuilds
// the parts that are not stored (the parsed dice expressions of a pending
// foe).
func (g *Game) validateSave(s *SaveFile) error {
	switch {
	case s.Version != saveVersion:
		return fmt.Errorf("save version %d, this build reads version %d", s.Version, saveVersion)
	case s.Character == nil:
		return errors.New("the save has no character")
	case s.Character.Name == "":
		return errors.New("the save character has no name")
	case s.Character.MaxHP <= 0:
		return errors.New("the save character has no hit points")
	case s.Character.Level <= 0:
		return errors.New("the save character has no level")
	case !g.World.Has(s.Location):
		return fmt.Errorf("the save is in region %q, which this world does not contain", s.Location)
	}

	if s.Character.HP > s.Character.MaxHP {
		s.Character.HP = s.Character.MaxHP
	}
	if s.Character.HP < 0 {
		s.Character.HP = 0
	}
	if s.Character.Weapon.DamageDice == "" {
		s.Character.Weapon = StartingWeapon()
	}
	if s.Foe != nil {
		if err := s.Foe.prepare(); err != nil {
			return fmt.Errorf("the saved encounter is unreadable: %w", err)
		}
	}
	return nil
}

// SaveExists reports whether a save file is present, which is how the
// -continue flag decides what to do.
func SaveExists(path string) bool {
	if path == "" {
		path = DefaultSavePath
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
