//go:build darwin

package git

import (
	"context"
	"os"
	"strings"

	"github.com/romus/space/internal/scan"
	"github.com/romus/space/pkg/recipe"
)

// worktree is one entry of "git worktree list --porcelain".
type worktree struct {
	path     string
	branch   string // short name, "" when detached
	locked   bool
	prunable bool // git itself says its folder is gone
}

// parseWorktrees reads the porcelain list. The first entry is the
// repository's own folder, which is not a linked worktree.
func parseWorktrees(out string) []worktree {
	var all []worktree
	for _, block := range strings.Split(out, "\n\n") {
		var w worktree
		for _, line := range lines(block) {
			key, value, _ := strings.Cut(line, " ")
			switch key {
			case "worktree":
				w.path = value
			case "branch":
				w.branch = strings.TrimPrefix(value, heads)
			case "locked":
				w.locked = true
			case "prunable":
				w.prunable = true
			}
		}
		if w.path != "" {
			all = append(all, w)
		}
	}
	if len(all) == 0 {
		return nil
	}
	return all[1:]
}

// The states of a linked worktree, stored in Item.Meta[MetaWhy].
const (
	WhyMissing = "missing" // its folder is gone; only git's record is left
	WhyClean   = "clean"   // nothing uncommitted in it
	WhyChanges = "changes" // has uncommitted or untracked files
	WhyLocked  = "locked"  // locked with git worktree lock
)

// worktreeItems describes the linked worktrees of a repository. One whose
// folder is gone is picked by default; a clean one can be marked by hand;
// the rest stay.
func (r *Recipe) worktreeItems(ctx context.Context, repo string, list []worktree) []recipe.Item {
	var items []recipe.Item
	for _, w := range list {
		it := recipe.Item{
			ID: "w/" + w.path, Group: repo, Path: w.path, Title: w.path, Kind: recipe.Special,
			Meta: map[string]string{MetaKind: KindWorktree, MetaBranch: w.branch},
		}
		fi, err := os.Stat(w.path)
		switch {
		case w.prunable || err != nil:
			it.Meta[MetaWhy] = WhyMissing
			it.Default = true
		case w.locked:
			it.Meta[MetaWhy], it.Locked = WhyLocked, "locked with git worktree lock"
		default:
			it.ModTime = fi.ModTime()
			it.Bytes = scan.SizeOf(w.path)
			it.Meta[MetaWhy] = WhyClean
			if out, err := r.read(ctx, w.path, "status", "--porcelain"); err != nil || out != "" {
				it.Meta[MetaWhy], it.Locked = WhyChanges, "has uncommitted or untracked files"
			}
		}
		items = append(items, it)
	}
	return items
}
