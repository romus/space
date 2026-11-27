//go:build darwin

package screens

import (
	"fmt"
	"image/color"
	"path/filepath"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/romus/space/internal/humanize"
	"github.com/romus/space/internal/recipes/git"
	"github.com/romus/space/internal/ui/kit"
	"github.com/romus/space/pkg/recipe"
)

// repoLines draws one repository: its local branches, the remote-tracking
// ones, and its worktrees, each with the reason it stays or can go.
func (g *Git) repoLines() []string {
	c, t := g.c, g.c.Theme
	w := c.W
	muted, faint := t.S(t.Muted), t.S(t.Faint)
	repo := g.repos[g.open]

	about := ".git " + humanize.Bytes(repo.GitBytes)
	if repo.Origin != "" {
		about += " · origin " + repo.Origin
	}
	top := []string{
		header(c, []kit.Span{
			kit.Sp(muted, "Recipes ›"), kit.Sp(t.S(t.Recipe), " ◆ Git"), kit.Sp(muted, " › "), kit.Sp(t.S(t.Strong), repo.Name),
		}, []kit.Span{kit.Sp(muted, about)}),
		kit.Blank(t, w),
	}
	line, rule := kit.Tabs(t, w, g.tabs(repo), g.tab, []kit.Span{kit.Sp(muted, g.tabHint(repo))})
	top = append(top, line, rule)

	heads := [][3]string{{"BRANCH", "WHY", "LAST COMMIT"}, {"BRANCH", "WHY", "LAST COMMIT"}, {"WORKTREE", "WHY", "SIZE"}}[g.tab]
	whyW := max(20, min(34, w/3))
	nameW := max(16, w-4-whyW-14-2)
	top = append(top, kit.Row(w, t.Base(), []kit.Span{
		kit.Cell(faint, "", 4), kit.Cell(faint, heads[0], nameW), kit.Cell(faint, "", 2), kit.Cell(faint, heads[1], whyW),
	}, []kit.Span{kit.CellR(faint, heads[2], 13), kit.Cell(faint, "", 1)}))

	rows := g.rows()
	g.rcur = min(g.rcur, max(0, len(rows)-1))
	visible := max(1, c.H-len(top)-5)
	g.roff = kit.Window(g.rcur, g.roff, len(rows), visible)
	for i := g.roff; i < len(rows) && i < g.roff+visible; i++ {
		top = append(top, g.repoRow(rows[i], i == g.rcur, nameW, whyW))
	}
	if len(rows) == 0 {
		top = append(top, kit.Blank(t, w), kit.Row(w, t.Base(), []kit.Span{kit.Sp(muted, "  Nothing here.")}, nil))
	}

	bottom := []string{kit.Rule(t, w)}
	if msg := g.toast.get(); msg != "" {
		bottom = append(bottom, kit.Row(w, t.Base(), []kit.Span{kit.Sp(t.S(t.Strong), msg)}, nil), kit.Blank(t, w))
	} else if repo.Skip != "" {
		// A repository that is left alone can be looked at, and that is all.
		bottom = append(bottom,
			kit.Row(w, t.Base(), []kit.Span{
				kit.Sp(t.S(t.Danger), "Left alone: "+repo.Skip+"."), kit.Sp(muted, " Nothing here will be touched, and nothing can be marked."),
			}, nil),
			kit.Row(w, t.Base(), []kit.Span{kit.Sp(muted, "Sort that out in the repository, then open space again.")}, nil))
	} else {
		bottom = append(bottom, g.tabNotes()...)
	}
	n := g.taken(repo.Path, git.KindBranch)
	gain := "nothing comes back without compacting"
	if g.pack {
		gain = "≈ " + humanize.Bytes(g.reclaim(repo.Path))
	}
	sum := []kit.Span{kit.Sp(muted, plural(n, "branch", "branches")+" · "), kit.Sp(t.S(t.Strong), gain)}
	if repo.Skip != "" {
		sum = []kit.Span{kit.Sp(muted, "left alone · "+repo.Skip)}
	}
	back := "all repos"
	if g.single {
		back = "back"
	}
	bottom = append(bottom, kit.Blank(t, w), kit.Footer(t, w,
		[]kit.Hint{{"␣", "mark"}, {"m", "merged only"}, {"→", "open group"}, {"tab", "next tab"}, {"esc", back}},
		sum, "⏎", "plan"))
	return kit.Frame(t, w, c.H, top, bottom)
}

func (g *Git) tabs(repo git.Repo) []kit.Tab {
	remote := fmt.Sprint(len(g.of(repo.Path, git.KindRemote)))
	stale := 0
	for _, it := range g.of(repo.Path, git.KindRemote) {
		if it.Meta[git.MetaWhy] == git.WhyStale {
			stale++
		}
	}
	if stale > 0 {
		remote = fmt.Sprintf("%d stale", stale)
	}
	return []kit.Tab{
		{Label: "Local", Badge: fmt.Sprint(len(g.of(repo.Path, git.KindBranch)))},
		{Label: "Remote-tracking", Badge: remote, Mark: stale > 0},
		{Label: "Worktrees", Badge: fmt.Sprint(len(g.of(repo.Path, git.KindWorktree)))},
	}
}

// tabHint is the right side of the tab bar.
func (g *Git) tabHint(repo git.Repo) string {
	switch {
	case g.tab == gitLocal:
		return "sorted by what’s safest to remove"
	case g.tab == gitRemote && g.asking[repo.Path]:
		return "asking origin which branches it still has…"
	case g.tab == gitRemote && g.head(repo.Path).Meta[git.MetaRemote] == git.RemoteUnreachable:
		return "origin did not answer: nothing here can be told stale"
	}
	return ""
}

// tabNotes are the two lines of explanation under a tab.
func (g *Git) tabNotes() []string {
	t := g.c.Theme
	w := g.c.W
	muted := t.S(t.Muted)
	row := func(spans ...kit.Span) string { return kit.Row(w, t.Base(), spans, nil) }
	switch g.tab {
	case gitRemote:
		return []string{
			row(kit.Sp(t.S(t.Safe), "stale"), kit.Sp(muted, " — a branch origin no longer has; only your local record of it is left.")),
			row(kit.Sp(muted, "Nothing is fetched and nothing is deleted on origin.")),
		}
	case gitWorktrees:
		return []string{
			row(kit.Sp(muted, "A worktree goes to the Trash, so it can be put back. Only one with nothing uncommitted can be marked.")),
			row(kit.Sp(muted, "Its branch stays.")),
		}
	}
	return []string{
		row(kit.Sp(t.S(t.Danger), "only here"), kit.Sp(muted, " — commits that exist on no remote. Never preselected; mark them yourself if you’re sure.")),
		row(kit.Sp(muted, "Branches that are not merged are saved to a bundle first, so you can bring them back.")),
	}
}

// repoRow draws one row of a tab: a branch, a fold of branches, or a
// worktree.
func (g *Git) repoRow(row gitRow, cur bool, nameW, whyW int) string {
	c, t := g.c, g.c.Theme
	st := func(fg color.Color) lipgloss.Style {
		if cur {
			return t.Sel(fg)
		}
		return t.S(fg)
	}
	first := row.items[0]
	name := first.Title
	if first.Meta[git.MetaKind] == git.KindWorktree {
		name = c.Tilde(first.Path)
	}
	if row.fold != "" {
		name = fmt.Sprintf("%s*  × %d", row.fold, len(row.items))
	}
	why, wc, kept := g.verdict(first)
	all, any := g.state(row)
	box, bc, nc := "[ ]", t.Muted, t.Text
	switch {
	case kept:
		box, bc, nc = " ○ ", t.Faint, t.Muted
	case all:
		box, bc = "[✓]", t.Strong
	case any:
		box, bc = "[–]", t.Strong
	}
	if cur && !kept {
		nc = t.Strong
	}
	last := g.ages(row.items)
	if first.Meta[git.MetaKind] == git.KindWorktree {
		last = "—"
		if first.Bytes > 0 {
			last = humanize.Bytes(first.Bytes)
		}
	}
	return kit.Row(c.W, st(t.Text), []kit.Span{
		kit.Cell(st(bc), box, 4), kit.Cell(st(nc), name, nameW), kit.Cell(st(t.Text), "", 2), kit.Cell(st(wc), why, whyW),
	}, []kit.Span{kit.CellR(st(t.Muted), last, 13), kit.Cell(st(t.Text), "", 1)})
}

// verdict says in a few words why an item stays or can go, in which colour,
// and whether it stays whatever the user does.
func (g *Git) verdict(it recipe.Item) (text string, fg color.Color, kept bool) {
	t := g.c.Theme
	why := it.Meta[git.MetaWhy]
	switch it.Meta[git.MetaKind] {
	case git.KindRemote:
		switch why {
		case git.WhyStale:
			return "gone on origin", t.Safe, false
		case git.WhyLive:
			return "still on origin", t.Muted, true
		case git.WhyDefault:
			return "kept · default", t.Muted, true
		}
		return it.Locked, t.Muted, true
	case git.KindWorktree:
		switch why {
		case git.WhyMissing:
			return "its folder is gone", t.Safe, false
		case git.WhyClean:
			return "clean · on " + it.Meta[git.MetaBranch], t.Text, false
		case git.WhyChanges:
			return "has changes", t.Muted, true
		}
		return "locked", t.Muted, true
	}
	repo := g.repos[g.open]
	switch {
	case why == git.WhyDefault:
		return "kept · default", t.Muted, true
	case why == git.WhyCurrent:
		return "kept · you are here", t.Muted, true
	case why == git.WhyWorktree:
		return "kept · in worktree " + filepath.Base(it.Path), t.Muted, true
	case git.Kept(it.Title, g.keep):
		return "kept · rule", t.Muted, true
	case why == git.WhyMerged:
		return "merged into " + repo.Default, t.Safe, false
	case why == git.WhyGone:
		return "gone on origin", t.Safe, false
	case why == git.WhyOnly:
		return "only here · " + plural(atoi(it.Meta[git.MetaOnly]), "commit", "commits"), t.Danger, false
	}
	return "pushed to origin", t.Safe, false
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// ages is when a row was last committed to; a fold shows its range.
func (g *Git) ages(items []recipe.Item) string {
	now := g.c.Now()
	var oldest, newest time.Time
	for _, it := range items {
		if it.ModTime.IsZero() || it.ModTime.Unix() <= 0 {
			continue
		}
		if oldest.IsZero() || it.ModTime.Before(oldest) {
			oldest = it.ModTime
		}
		if it.ModTime.After(newest) {
			newest = it.ModTime
		}
	}
	if newest.IsZero() {
		return "—"
	}
	a, b := humanize.Age(newest, now), humanize.Age(oldest, now)
	if a == b {
		return a
	}
	return a + " – " + b
}
