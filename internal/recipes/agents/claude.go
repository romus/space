//go:build darwin

package agents

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/romus/space/internal/scan"
	"github.com/romus/space/pkg/recipe"
)

// ClaudeID is the recipe id of the Claude Code recipe.
const ClaudeID = "claude"

func init() { recipe.Register(ClaudeID, func() recipe.Recipe { return &Claude{} }) }

// Claude cleans old Claude Code sessions under ~/.claude.
type Claude struct {
	env     recipe.Env
	running bool
}

func (c *Claude) ID() string         { return ClaudeID }
func (c *Claude) dir() string        { return filepath.Join(c.env.Home, ".claude") }
func (c *Claude) Running() bool      { return c.running }
func (c *Claude) BinaryName() string { return "claude" }

func (c *Claude) NeverTouch() []string {
	return []string{"settings.json", "CLAUDE.md", "agents/", "commands/", "skills/", "plugins/", "memory/"}
}

// Scratch folders whose old entries are safe to delete. Anything not listed
// here or reachable from a session is left alone.
var claudeExtras = []string{"todos", "shell-snapshots", "debug", "paste-cache"}

// Per-session folders outside the project folder, named after the session.
var claudeSessionDirs = []string{"file-history", "session-env", "tasks"}

func (c *Claude) Info() recipe.Info {
	return recipe.Info{
		Name:   "Claude Code",
		Path:   c.dir(),
		Source: "built-in",
		Hint:   "Claude Code",
		Advice: "Don’t rm this — it also holds your settings, memory and skills. Press r for the recipe: remove old sessions only.",
		About: "Removes session transcripts you have not used for a while, with their sub-agent logs, file " +
			"checkpoints and scratch files. Sessions go to the Trash. Settings, CLAUDE.md, memory, agents, " +
			"commands, skills and plugins are never touched, and neither is a session that is open right now.",
	}
}

func (c *Claude) Detect(ctx context.Context, env recipe.Env) bool {
	c.env = env
	st, err := os.Stat(filepath.Join(c.dir(), "projects"))
	return err == nil && st.IsDir()
}

// activeSessions reads the pid files Claude Code keeps for live sessions.
func (c *Claude) activeSessions() map[string]bool {
	active := map[string]bool{}
	files, _ := filepath.Glob(filepath.Join(c.dir(), "sessions", "*.json"))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var s struct {
			PID       int    `json:"pid"`
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(data, &s) != nil || !alive(s.PID) {
			continue
		}
		active[s.SessionID] = true
	}
	return active
}

func (c *Claude) Inspect(ctx context.Context) ([]recipe.Item, error) {
	base := c.dir()
	active := c.activeSessions()
	c.running = len(active) > 0

	projects, err := os.ReadDir(filepath.Join(base, "projects"))
	if err != nil {
		return nil, err
	}
	var items []recipe.Item
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		pdir := filepath.Join(base, "projects", p.Name())
		entries, err := os.ReadDir(pdir)
		if err != nil {
			continue
		}
		var (
			sessions []recipe.Item
			cwd      string
			newest   int64
		)
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			id := strings.TrimSuffix(e.Name(), ".jsonl")
			path := filepath.Join(pdir, e.Name())
			it := recipe.Item{
				ID:      id,
				Path:    path,
				Bytes:   scan.SizeOf(path),
				Kind:    recipe.Special,
				ModTime: info.ModTime(),
				Default: true,
				Meta:    map[string]string{},
			}
			// Everything named after the session goes with it.
			var with []string
			candidates := []string{filepath.Join(pdir, id)}
			for _, d := range claudeSessionDirs {
				candidates = append(candidates, filepath.Join(base, d, id))
			}
			candidates = append(candidates, filepath.Join(base, "debug", id+".txt"))
			for _, cand := range candidates {
				if _, err := os.Lstat(cand); err == nil {
					with = append(with, cand)
					it.Bytes += scan.SizeOf(cand)
				}
			}
			it.Meta["with"] = strings.Join(with, "\n")
			if active[id] {
				it.Locked = "open now"
			}
			if t := info.ModTime().UnixNano(); t > newest {
				newest = t
				if found := findCwd(path); found != "" {
					cwd = found
				}
			}
			sessions = append(sessions, it)
		}
		if cwd == "" {
			// No transcript mentions it: fall back to the folder name.
			cwd = p.Name()
			for _, s := range sessions {
				if found := findCwd(s.Path); found != "" {
					cwd = found
					break
				}
			}
		}
		for i := range sessions {
			sessions[i].Group = p.Name()
			sessions[i].Title = cwd
		}
		items = append(items, sessions...)
	}
	for _, name := range claudeExtras {
		for _, it := range extras(base, name) {
			// A debug file named after a session is removed with it.
			if name == "debug" && sessionOwned(items, it.Path) {
				continue
			}
			items = append(items, it)
		}
	}
	return items, nil
}

func sessionOwned(items []recipe.Item, path string) bool {
	for _, it := range items {
		for _, w := range companions(it) {
			if w == path {
				return true
			}
		}
	}
	return false
}

func (c *Claude) Summary(items []recipe.Item) string { return summary(items, c.env.Now()) }

func (c *Claude) Plan(sel []recipe.Item, opt recipe.Options) (recipe.Plan, error) {
	return buildPlan(spec{id: ClaudeID, name: "Claude Code", dir: c.dir()}, sel, opt, c.env.Home)
}
