// Package config reads the user's settings and remembers small bits of
// state between runs. It only ever writes under ~/.config/space and
// ~/.local/state/space.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Paths says where space keeps its files.
type Paths struct {
	Home      string
	ConfigDir string // ~/.config/space
	StateDir  string // ~/.local/state/space
}

// Resolve works out the directories. SPACE_HOME replaces the home directory
// entirely, which is how tests and sandboxes keep away from real data.
func Resolve() (Paths, error) {
	home := os.Getenv("SPACE_HOME")
	sandbox := home != ""
	if !sandbox {
		h, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, err
		}
		home = h
	}
	home, err := filepath.Abs(home)
	if err != nil {
		return Paths{}, err
	}
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	cfg := filepath.Join(home, ".config")
	state := filepath.Join(home, ".local", "state")
	if !sandbox {
		if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
			cfg = v
		}
		if v := os.Getenv("XDG_STATE_HOME"); v != "" {
			state = v
		}
	}
	return Paths{
		Home:      home,
		ConfigDir: filepath.Join(cfg, "space"),
		StateDir:  filepath.Join(state, "space"),
	}, nil
}

// Sandboxed reports whether SPACE_HOME is in effect.
func Sandboxed() bool { return os.Getenv("SPACE_HOME") != "" }

// RecipeRoot is the opened folder as recipes may see it. A sandboxed run
// that was pointed outside its sandbox gets none: a recipe that works on
// the opened folder must not wander into real data.
func (p Paths) RecipeRoot(dir string) string {
	if Sandboxed() && dir != p.Home && !strings.HasPrefix(dir, p.Home+string(filepath.Separator)) {
		return ""
	}
	return dir
}

// LogFile is today's log: ~/.local/state/space/2026-11-22.log.
func (p Paths) LogFile(now time.Time) string {
	return filepath.Join(p.StateDir, now.Format("2006-01-02")+".log")
}

// RecipeDir is where declarative recipes will live.
func (p Paths) RecipeDir() string { return filepath.Join(p.ConfigDir, "recipes") }

// Config is config.toml, written by the user.
type Config struct {
	Theme string `toml:"theme"` // system | light | dark
}

// Load reads config.toml. A missing file is not an error.
func Load(p Paths) (Config, error) {
	var c Config
	path := filepath.Join(p.ConfigDir, "config.toml")
	if _, err := toml.DecodeFile(path, &c); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, err
	}
	return c, nil
}

// State is state.json, written by space itself.
type State struct {
	Theme    string              `json:"theme,omitempty"`     // last theme picked with t
	KeepDays int                 `json:"keep_days,omitempty"` // AI agents keep window
	Pinned   map[string][]string `json:"pinned,omitempty"`    // recipe id → pinned projects
	Hidden   []string            `json:"hidden,omitempty"`    // recipes hidden with x
	GitKeep  []string            `json:"git_keep,omitempty"`  // Git: branch names that stay

	path string
}

// LoadState reads state.json. A missing or broken file gives empty state.
func LoadState(p Paths) *State {
	s := &State{path: filepath.Join(p.StateDir, "state.json")}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return s
	}
	if json.Unmarshal(data, s) != nil {
		*s = State{path: s.path}
	}
	return s
}

// Save writes state.json atomically. Failing to save state is never fatal.
func (s *State) Save() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// IsHidden reports whether the user hid a recipe.
func (s *State) IsHidden(id string) bool {
	for _, h := range s.Hidden {
		if h == id {
			return true
		}
	}
	return false
}

// SetHidden hides or shows a recipe.
func (s *State) SetHidden(id string, hidden bool) {
	out := s.Hidden[:0:0]
	for _, h := range s.Hidden {
		if h != id {
			out = append(out, h)
		}
	}
	if hidden {
		out = append(out, id)
	}
	s.Hidden = out
}

// PinnedSet returns the pinned projects of a recipe as a set.
func (s *State) PinnedSet(id string) map[string]bool {
	set := map[string]bool{}
	for _, p := range s.Pinned[id] {
		set[p] = true
	}
	return set
}

// SetPinned pins or unpins a project.
func (s *State) SetPinned(id, project string, pinned bool) {
	if s.Pinned == nil {
		s.Pinned = map[string][]string{}
	}
	var out []string
	for _, p := range s.Pinned[id] {
		if p != project {
			out = append(out, p)
		}
	}
	if pinned {
		out = append(out, project)
	}
	s.Pinned[id] = out
}
