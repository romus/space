// Package recipe is the contract between space and the things it knows how
// to clean in a special way.
//
// A recipe never deletes anything itself. It detects, inspects and returns a
// plan made of steps; only the engine executes steps. That is why every
// recipe gets a preview, --dry-run, "copy as script", a log and a stop
// between steps for free.
package recipe

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Kind says how a thing gets removed.
type Kind int

const (
	Plain   Kind = iota // ordinary files: go to the Trash
	Cache               // safe to delete, will be rebuilt
	Special             // owned by a recipe: needs the right order of commands
)

// Glyph is the one-character mark shown next to a row. Glyphs carry the same
// meaning as colour, so the UI stays readable without colour.
func (k Kind) Glyph() string {
	switch k {
	case Cache:
		return "●"
	case Special:
		return "◆"
	}
	return ""
}

// Runner runs read-only commands for Detect and Inspect.
type Runner interface {
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
	LookPath(name string) (string, error)
}

// Env is everything a recipe may know about the machine.
type Env struct {
	Home string // home directory; tests and SPACE_HOME point it at a sandbox
	Run  Runner
	Now  func() time.Time
}

// Item is one removable thing found by Inspect.
type Item struct {
	ID      string // stable within the recipe
	Group   string // section the item belongs to ("images", a project, ...)
	Path    string
	Title   string
	Bytes   int64             // on disk, st_blocks × 512
	Kind    Kind              // Plain | Cache | Special
	Locked  string            // "api-dev · running": cannot be selected
	ModTime time.Time         // last used / created
	Default bool              // part of the default selection
	Meta    map[string]string // recipe-specific columns
}

// Remove is a deletion performed by the engine itself, with its safety
// checks, instead of shelling out to rm.
type Remove struct {
	Paths   []string
	ToTrash bool
	Root    string // every path must live strictly inside Root
	Bytes   int64  // total size, for reporting
	// Inodes is filled in when the plan is shown: the engine refuses a path
	// that is no longer the same file when the plan runs.
	Inodes []uint64
}

// Step is one thing the engine will do.
type Step struct {
	Title string   // in human words
	Cmd   []string // exactly what will run
	Why   string

	// Remove, when set, replaces Cmd: the engine deletes the paths itself and
	// Cmd is not used. The UI and scripts show an equivalent shell command.
	Remove *Remove

	Reversible bool
	Safe       bool // irreversible in theory, harmless in practice
	ReadOnly   bool
	Sudo       bool
}

// Tag is the label shown next to a step.
func (s Step) Tag() string {
	switch {
	case s.ReadOnly:
		return "READ-ONLY"
	case s.Safe:
		return "SAFE"
	case s.Reversible:
		return "REVERSIBLE"
	}
	return "IRREVERSIBLE"
}

// Irreversible reports whether the step destroys something for good.
func (s Step) Irreversible() bool { return s.Tag() == "IRREVERSIBLE" }

// Toggle is a switch on the plan screen; flipping it rebuilds the plan.
type Toggle struct {
	Key   string
	Label string
	On    bool
}

// Metric is one number measured before and after a run.
type Metric struct {
	Label  string // "machine disk on Mac"
	Before int64
	After  int64 // estimate while planning, measured once done
}

// Action is a follow-up offered when a run is done.
type Action struct {
	Key   string
	Label string
	Plan  Plan
}

// Plan is what will happen, in order.
type Plan struct {
	RecipeID string
	Title    string // breadcrumb: "Podman"
	Steps    []Step
	Toggles  []Toggle
	Reclaim  int64 // bytes expected back on the Mac
	ToTrash  int64 // bytes that only move to the Trash
	Seconds  int   // rough duration
	Metric   *Metric
	Summary  []string // lines for the done screen: "images  14 → 8"
	After    []Action
}

// Options carries the user's choices into Plan.
type Options struct {
	Toggles   map[string]bool
	KeepDays  int             // AI agents: keep sessions newer than this
	Pinned    map[string]bool // AI agents: projects kept entirely
	Permanent bool            // delete for good instead of the Trash
}

// Toggle returns the value of a toggle, or def when the user never set it.
func (o Options) Toggle(key string, def bool) bool {
	if v, ok := o.Toggles[key]; ok {
		return v
	}
	return def
}

// Info describes a recipe to the user.
type Info struct {
	Name   string // "Podman"
	Path   string // the folder it owns, absolute
	Source string // "built-in"
	Hint   string // short label for folder rows: "podman machine"
	Advice string // why not to rm it by hand
	About  string // what the recipe does, a few sentences
}

// Recipe knows how to clean one thing that cannot simply be deleted.
type Recipe interface {
	ID() string
	Info() Info
	// Detect is fast and has no side effects. It also hands the recipe the
	// environment it will use for Inspect.
	Detect(ctx context.Context, env Env) bool
	// Inspect lists what is inside, with sizes and what is in use.
	Inspect(ctx context.Context) ([]Item, error)
	// Summary says what was found, in one line.
	Summary(items []Item) string
	// Plan turns a selection into steps.
	Plan(sel []Item, opt Options) (Plan, error)
}

// Measurer is implemented by recipes that track their own number, like the
// size of a VM disk, next to free space on the volume.
type Measurer interface {
	Measure(ctx context.Context) (label string, bytes int64, ok bool)
}

var (
	mu       sync.Mutex
	registry = map[string]func() Recipe{}
)

// Register adds a built-in recipe. Call it from init().
func Register(id string, factory func() Recipe) {
	mu.Lock()
	defer mu.Unlock()
	registry[id] = factory
}

// All returns a fresh instance of every registered recipe, sorted by ID.
func All() []Recipe {
	mu.Lock()
	defer mu.Unlock()
	ids := make([]string, 0, len(registry))
	for id := range registry {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Recipe, 0, len(ids))
	for _, id := range ids {
		out = append(out, registry[id]())
	}
	return out
}

// DefaultSelection returns the items a recipe would pick on its own.
func DefaultSelection(items []Item) []Item {
	var sel []Item
	for _, it := range items {
		if it.Default && it.Locked == "" {
			sel = append(sel, it)
		}
	}
	return sel
}

// Sum adds up the sizes of items.
func Sum(items []Item) int64 {
	var n int64
	for _, it := range items {
		n += it.Bytes
	}
	return n
}
