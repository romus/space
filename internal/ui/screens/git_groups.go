//go:build darwin

package screens

import (
	"sort"
	"strings"

	"github.com/romus/space/internal/recipes/git"
	"github.com/romus/space/pkg/recipe"
)

// foldAt is how many branches with the same prefix and the same reason
// become one row: "dependabot/npm_and_yarn/* × 12".
const foldAt = 3

// gitRow is one row of a repository's tab: an item, or several folded.
type gitRow struct {
	items []recipe.Item
	fold  string // "dependabot/npm_and_yarn/" when the row is a fold
}

// rank orders the rows as the design does: what stays first, then what is
// safest to remove, and last what exists only here.
func (g *Git) rank(it recipe.Item) int {
	switch it.Meta[git.MetaKind] {
	case git.KindBranch:
		if it.Locked != "" || git.Kept(it.Title, g.keep) {
			return 0
		}
		switch it.Meta[git.MetaWhy] {
		case git.WhyMerged:
			return 1
		case git.WhyGone:
			return 2
		case git.WhyPushed:
			return 3
		}
		return 4
	case git.KindRemote:
		if it.Meta[git.MetaWhy] == git.WhyStale {
			return 1
		}
		return 0
	case git.KindWorktree:
		switch it.Meta[git.MetaWhy] {
		case git.WhyMissing:
			return 0
		case git.WhyClean:
			return 1
		}
		return 2
	}
	return 0
}

// prefix is the part of a branch name its relatives share: "release/" for
// "release/2025.1", "" for a name without a slash.
func prefix(name string) string {
	return name[:strings.LastIndex(name, "/")+1]
}

func gitFoldKey(it recipe.Item, rank int) string {
	return it.Group + "\x00" + prefix(it.Title) + "\x00" + string(rune('0'+rank))
}

// rows are the rows of the open repository's tab, sorted and folded.
func (g *Git) rows() []gitRow {
	kind := []string{git.KindBranch, git.KindRemote, git.KindWorktree}[g.tab]
	items := g.of(g.repos[g.open].Path, kind)
	sort.SliceStable(items, func(i, j int) bool {
		if a, b := g.rank(items[i]), g.rank(items[j]); a != b {
			return a < b
		}
		return items[i].Title < items[j].Title
	})
	var rows []gitRow
	for i := 0; i < len(items); {
		rank, pre := g.rank(items[i]), prefix(items[i].Title)
		j := i + 1
		for j < len(items) && g.rank(items[j]) == rank && prefix(items[j].Title) == pre {
			j++
		}
		// What stays is never folded: each row says why it stays.
		folds := kind != git.KindWorktree && pre != "" && rank > 0 && j-i >= foldAt &&
			!g.expanded[gitFoldKey(items[i], rank)]
		if folds {
			rows = append(rows, gitRow{items: items[i:j], fold: pre})
		} else {
			for _, it := range items[i:j] {
				rows = append(rows, gitRow{items: []recipe.Item{it}})
			}
		}
		i = j
	}
	return rows
}

// state says how much of a row is chosen: all, some or none of it.
func (g *Git) state(row gitRow) (all, any bool) {
	all = true
	for _, it := range row.items {
		if g.chosen(it) {
			any = true
		} else {
			all = false
		}
	}
	return all, any
}
