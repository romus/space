//go:build darwin

// Package agents cleans up after AI coding agents: old Claude Code and
// Codex sessions and the leftovers around them.
//
// Both recipes work from an allow-list. Only session transcripts and a few
// named scratch folders are ever offered; settings, memory, skills and
// everything space does not recognise are never touched.
package agents

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/romus/space/internal/humanize"
	"github.com/romus/space/internal/scan"
	"github.com/romus/space/pkg/recipe"
)

// DefaultKeepDays is the keep window used until the user picks another.
const DefaultKeepDays = 30

// KeepWindows are the choices offered on the screen, in days.
var KeepWindows = []int{1, 7, 14, 30, 60, 90, 180}

// ExtraPrefix marks the group of an item that is a leftover, not a session.
const ExtraPrefix = "+"

// IsExtra reports whether a group holds leftovers rather than sessions.
func IsExtra(group string) bool { return strings.HasPrefix(group, ExtraPrefix) }

// Source is implemented by both agent recipes for the shared screen.
type Source interface {
	recipe.Recipe
	// Running reports whether the agent has live processes right now.
	Running() bool
	// NeverTouch lists what the recipe leaves alone, for display.
	NeverTouch() []string
	// BinaryName is the process name shown in "claude is running".
	BinaryName() string
}

// Select applies the keep window and pins to the inspected items: it returns
// what would be removed. excluded holds groups the user unchecked.
func Select(items []recipe.Item, keepDays int, pinned, excluded map[string]bool, now time.Time) []recipe.Item {
	cutoff := now.Add(-time.Duration(keepDays) * 24 * time.Hour)
	var out []recipe.Item
	for _, it := range items {
		if it.Locked != "" || pinned[it.Group] || excluded[it.Group] {
			continue
		}
		if it.ModTime.After(cutoff) {
			continue
		}
		out = append(out, it)
	}
	return out
}

// findCwd pulls the working directory out of a session transcript without
// decoding it: transcripts can be hundreds of megabytes.
func findCwd(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, 256*1024)
	n, _ := f.Read(buf)
	buf = buf[:n]
	key := []byte(`"cwd":"`)
	i := bytes.Index(buf, key)
	if i < 0 {
		return ""
	}
	rest := buf[i+len(key):]
	var out []byte
	for j := 0; j < len(rest); j++ {
		switch rest[j] {
		case '\\':
			if j+1 < len(rest) {
				j++
				out = append(out, rest[j])
			}
		case '"':
			return string(out)
		default:
			out = append(out, rest[j])
		}
	}
	return ""
}

// extras lists the entries of a scratch folder as leftover items.
func extras(base, name string) []recipe.Item {
	dir := filepath.Join(base, name)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []recipe.Item
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(dir, e.Name())
		out = append(out, recipe.Item{
			ID:      name + "/" + e.Name(),
			Group:   ExtraPrefix + name,
			Path:    path,
			Title:   name + "/",
			Bytes:   scan.SizeOf(path),
			Kind:    recipe.Cache,
			ModTime: info.ModTime(),
			Default: true,
		})
	}
	return out
}

func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return isAlive(p)
}

// companions are extra paths removed together with an item.
func companions(it recipe.Item) []string {
	if it.Meta["with"] == "" {
		return nil
	}
	return strings.Split(it.Meta["with"], "\n")
}

type spec struct {
	id, name, dir string
}

const maxProjectSteps = 10

// buildPlan turns selected items into steps: one per project, plus one per
// scratch folder.
func buildPlan(sp spec, sel []recipe.Item, opt recipe.Options, home string) (recipe.Plan, error) {
	if len(sel) == 0 {
		return recipe.Plan{}, fmt.Errorf("nothing to remove: every session is inside the keep window")
	}
	keep := opt.KeepDays
	if keep <= 0 {
		keep = DefaultKeepDays
	}

	type group struct {
		name  string
		title string
		items []recipe.Item
		bytes int64
	}
	byName := map[string]*group{}
	var groups []*group
	for _, it := range sel {
		g := byName[it.Group]
		if g == nil {
			g = &group{name: it.Group, title: it.Title}
			byName[it.Group] = g
			groups = append(groups, g)
		}
		g.items = append(g.items, it)
		g.bytes += it.Bytes
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if IsExtra(groups[i].name) != IsExtra(groups[j].name) {
			return !IsExtra(groups[i].name)
		}
		return groups[i].bytes > groups[j].bytes
	})

	paths := func(items []recipe.Item) []string {
		var out []string
		for _, it := range items {
			out = append(out, it.Path)
			out = append(out, companions(it)...)
		}
		return out
	}

	p := recipe.Plan{RecipeID: sp.id, Title: sp.name}
	var sessions int

	var projects, leftovers []*group
	for _, g := range groups {
		if IsExtra(g.name) {
			leftovers = append(leftovers, g)
		} else {
			projects = append(projects, g)
		}
	}
	// Many small projects would bury the plan: fold the tail into one step.
	if len(projects) > maxProjectSteps {
		tail := &group{title: fmt.Sprintf("%d other projects", len(projects)-maxProjectSteps+1)}
		for _, g := range projects[maxProjectSteps-1:] {
			tail.items = append(tail.items, g.items...)
			tail.bytes += g.bytes
		}
		projects = append(projects[:maxProjectSteps-1:maxProjectSteps-1], tail)
	}

	for _, g := range projects {
		sessions += len(g.items)
		r := &recipe.Remove{Paths: paths(g.items), ToTrash: !opt.Permanent, Root: sp.dir, Bytes: g.bytes}
		step := recipe.Step{
			Title:  fmt.Sprintf("Remove %s from %s", humanize.Plural(len(g.items), "session"), humanize.Tilde(g.title, home)),
			Remove: r,
		}
		if opt.Permanent {
			step.Why = fmt.Sprintf("Not used for %d days or more. %s, deleted for good.", keep, humanize.Bytes(g.bytes))
			p.Reclaim += g.bytes
		} else {
			step.Why = fmt.Sprintf("Not used for %d days or more. %s goes to the Trash.", keep, humanize.Bytes(g.bytes))
			step.Reversible = true
			p.ToTrash += g.bytes
		}
		p.Steps = append(p.Steps, step)
	}
	for _, g := range leftovers {
		p.Steps = append(p.Steps, recipe.Step{
			Title:  "Clean " + strings.TrimPrefix(g.name, ExtraPrefix) + "/",
			Why:    fmt.Sprintf("Leftovers older than %d days. %s.", keep, humanize.Bytes(g.bytes)),
			Remove: &recipe.Remove{Paths: paths(g.items), Root: sp.dir, Bytes: g.bytes},
			Safe:   true,
		})
		p.Reclaim += g.bytes
	}

	p.Seconds = 2 + len(sel)/200
	if sessions > 0 {
		p.Summary = append(p.Summary, fmt.Sprintf("sessions removed\t%s", humanize.Count(int64(sessions))))
	}
	return p, nil
}

func summary(items []recipe.Item, now time.Time) string {
	projects := map[string]bool{}
	var sessions, old int
	cutoff := now.Add(-DefaultKeepDays * 24 * time.Hour)
	for _, it := range items {
		if IsExtra(it.Group) {
			continue
		}
		sessions++
		projects[it.Group] = true
		if it.ModTime.Before(cutoff) {
			old++
		}
	}
	return fmt.Sprintf("%s in %s · %s older than %d days",
		humanize.Plural(sessions, "session"), humanize.Plural(len(projects), "project"),
		humanize.Count(int64(old)), DefaultKeepDays)
}
