//go:build darwin

package agents

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/romus/space/internal/scan"
	"github.com/romus/space/pkg/recipe"
)

// CodexID is the recipe id of the Codex recipe.
const CodexID = "codex"

func init() { recipe.Register(CodexID, func() recipe.Recipe { return &Codex{} }) }

// Codex cleans old Codex sessions and logs under ~/.codex.
type Codex struct {
	env     recipe.Env
	running bool
}

func (c *Codex) ID() string         { return CodexID }
func (c *Codex) dir() string        { return filepath.Join(c.env.Home, ".codex") }
func (c *Codex) Running() bool      { return c.running }
func (c *Codex) BinaryName() string { return "codex" }

func (c *Codex) NeverTouch() []string {
	return []string{"config.toml", "auth.json", "AGENTS.md", "skills/", "prompts/", "rules/", "memories/", "*.sqlite"}
}

var codexExtras = []string{"log", "shell_snapshots"}

func (c *Codex) Info() recipe.Info {
	return recipe.Info{
		Name:   "Codex",
		Path:   c.dir(),
		Source: "built-in",
		Hint:   "Codex",
		Advice: "Don’t rm this — it also holds your login, config and memories. Press r for the recipe: remove old sessions and logs.",
		About: "Removes session rollouts you have not used for a while, and old log files. Sessions go to the " +
			"Trash. config.toml, auth.json, AGENTS.md, skills, prompts, rules, memories and the sqlite " +
			"databases are never touched.",
	}
}

func (c *Codex) Detect(ctx context.Context, env recipe.Env) bool {
	c.env = env
	st, err := os.Stat(c.dir())
	return err == nil && st.IsDir()
}

func (c *Codex) Inspect(ctx context.Context) ([]recipe.Item, error) {
	base := c.dir()
	c.running = false
	if out, err := c.env.Run.Output(ctx, "pgrep", "-x", "codex"); err == nil && len(strings.TrimSpace(string(out))) > 0 {
		c.running = true
	}

	now := c.env.Now()
	var items []recipe.Item
	root := filepath.Join(base, "sessions")
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		cwd := findCwd(path)
		if cwd == "" {
			cwd = "(unknown folder)"
		}
		it := recipe.Item{
			ID:      strings.TrimSuffix(d.Name(), ".jsonl"),
			Group:   cwd,
			Title:   cwd,
			Path:    path,
			Bytes:   scan.SizeOf(path),
			Kind:    recipe.Special,
			ModTime: info.ModTime(),
			Default: true,
			Meta:    map[string]string{},
		}
		// A rollout written in the last few minutes belongs to a live run.
		if c.running && now.Sub(info.ModTime()).Minutes() < 10 {
			it.Locked = "open now"
		}
		items = append(items, it)
		return nil
	})
	for _, name := range codexExtras {
		items = append(items, extras(base, name)...)
	}
	return items, nil
}

func (c *Codex) Summary(items []recipe.Item) string { return summary(items, c.env.Now()) }

func (c *Codex) Plan(sel []recipe.Item, opt recipe.Options) (recipe.Plan, error) {
	return buildPlan(spec{id: CodexID, name: "Codex", dir: c.dir()}, sel, opt, c.env.Home)
}
