//go:build darwin

package screens

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/romus/space/internal/humanize"
	"github.com/romus/space/internal/recipes/git"
	"github.com/romus/space/internal/ui/kit"
)

func (g *Git) Lines() []string {
	if g.open >= 0 {
		return g.repoLines()
	}
	return g.listLines()
}

// listLines draws every repository under the opened folder: one rule for
// all of them, and for each what would go and what it would give back.
func (g *Git) listLines() []string {
	c, t := g.c, g.c.Theme
	w := c.W
	muted, faint := t.S(t.Muted), t.S(t.Faint)

	found := plural(len(g.repos), "repository", "repositories")
	top := []string{
		header(c, []kit.Span{
			kit.Sp(muted, "Recipes ›"), kit.Sp(t.S(t.Recipe), " ◆ Git"), kit.Sp(muted, " · "+c.Tilde(c.Root)),
		}, []kit.Span{kit.Sp(muted, found+" found")}),
		kit.Blank(t, w),
	}
	top = append(top, kit.Box(t, w, g.ruleBox())...)

	noteW := max(0, min(56, w-(4+10+14+12+3)-24))
	nameW := max(16, w-(4+10+14+12+3)-noteW)
	top = append(top, kit.Blank(t, w), kit.Row(w, t.Base(), []kit.Span{
		kit.Cell(faint, "", 4), kit.Cell(faint, "REPOSITORY", nameW), kit.CellR(faint, ".GIT", 10),
		kit.CellR(faint, "BRANCHES OUT", 14), kit.CellR(faint, "RECLAIM ≈", 12),
		kit.Cell(faint, "", 3), kit.Cell(faint, "NOTE", noteW),
	}, nil))

	visible := max(1, c.H-len(top)-5)
	g.off = kit.Window(g.cur, g.off, len(g.repos), visible)
	for i := g.off; i < len(g.repos) && i < g.off+visible; i++ {
		top = append(top, g.listRow(i, nameW, noteW))
	}

	bottom := []string{kit.Rule(t, w)}
	if msg := g.toast.get(); msg != "" {
		bottom = append(bottom, kit.Row(w, t.Base(), []kit.Span{kit.Sp(t.S(t.Strong), msg)}, nil), kit.Blank(t, w))
	} else {
		bottom = append(bottom,
			kit.Row(w, t.Base(), []kit.Span{
				kit.Sp(muted, "Deleting a branch alone frees almost nothing: its commits stay in .git until the reflog expires."),
			}, nil),
			kit.Row(w, t.Base(), []kit.Span{
				kit.Sp(muted, "The plan deletes, expires and compacts in one go. Backups keep unmerged work for 30 days, and its space too."),
			}, nil))
	}
	repos, branches, bytes := g.totals()
	sum := []kit.Span{
		kit.Sp(muted, fmt.Sprintf("%s · %s · ", humanize.Plural(repos, "repo"), plural(branches, "branch", "branches"))),
		kit.Sp(t.S(t.Strong), "≈ "+humanize.Bytes(bytes)),
	}
	hints := []kit.Hint{{"␣", "include repo"}, {"→", "its branches"}, {"e", "rule"}, {"esc", "back"}}
	if g.editing {
		hints = []kit.Hint{{"enter", "save the rule"}, {"esc", "cancel"}}
	}
	bottom = append(bottom, kit.Blank(t, w), kit.Footer(t, w, hints, sum, "⏎", "plan"))
	return kit.Frame(t, w, c.H, top, bottom)
}

// ruleBox is the rule that applies to every repository, and the two
// things the plan does besides deleting branches.
func (g *Git) ruleBox() []string {
	t := g.c.Theme
	w := g.c.W - 4
	muted, text := t.S(t.Muted), t.S(t.Text)
	rule := []kit.Span{kit.Cell(muted, "keep", 8)}
	hint := []kit.Span{kit.Sp(text, "e"), kit.Sp(muted, " edit rule")}
	if g.editing {
		rule = append(rule, kit.Sp(t.S(t.Strong), g.input+"▍"))
		hint = []kit.Span{kit.Sp(muted, "names or patterns like release/*")}
	} else {
		rule = append(rule, kit.Sp(t.S(t.Strong), strings.Join(g.keep, " ")),
			kit.Sp(t.S(t.Faint), "  +  "), kit.Sp(text, "the main and current branch"),
			kit.Sp(t.S(t.Faint), "  +  "), kit.Sp(text, "anything not pushed"))
	}
	box := func(on bool) kit.Span {
		if on {
			return kit.Sp(t.S(t.Strong), "[✓]")
		}
		return kit.Sp(muted, "[ ]")
	}
	also := []kit.Span{
		kit.Cell(muted, "also", 8),
		kit.Sp(text, "p "), box(g.prune), kit.Sp(text, " prune stale origin refs    "),
		kit.Sp(text, "c "), box(g.pack), kit.Sp(text, " compact .git    "),
		kit.Sp(text, "b "), box(g.save), kit.Sp(text, " back up what is not merged"),
	}
	return []string{kit.Row(w, t.Base(), rule, hint), kit.Row(w, t.Base(), also, nil)}
}

// listRow draws one repository.
func (g *Git) listRow(i, nameW, noteW int) string {
	t := g.c.Theme
	repo := g.repos[i]
	head := g.head(repo.Path)
	st := func(fg color.Color) lipgloss.Style {
		if i == g.cur {
			return t.Sel(fg)
		}
		return t.S(fg)
	}
	box, bc, nc := "[ ]", t.Muted, t.Text
	out, gain := "—", "—"
	note, notec := g.note(repo)
	switch {
	case head.Locked != "":
		box, bc, nc = " ○ ", t.Faint, t.Muted
	case g.chosen(head):
		box, bc = "[✓]", t.Strong
		out = fmt.Sprintf("%d of %d", g.taken(repo.Path, git.KindBranch), len(g.of(repo.Path, git.KindBranch)))
		if g.pack {
			gain = humanize.Bytes(g.reclaim(repo.Path))
		}
	}
	if i == g.cur && head.Locked == "" {
		nc = t.Strong
	}
	return kit.Row(g.c.W, st(t.Text), []kit.Span{
		kit.Cell(st(bc), box, 4), kit.Cell(st(nc), repo.Name, nameW),
		kit.CellR(st(t.Muted), humanize.Bytes(repo.GitBytes), 10),
		kit.CellR(st(nc), out, 14), kit.CellR(st(nc), gain, 12),
		kit.Cell(st(t.Text), "", 3), kit.Cell(st(notec), note, noteW),
	}, nil)
}

// note is the one thing worth knowing about a repository.
func (g *Git) note(repo git.Repo) (string, color.Color) {
	t := g.c.Theme
	if repo.Skip != "" {
		return repo.Skip + " · skipped", t.Muted
	}
	var parts []string
	only := 0
	for _, it := range g.of(repo.Path, git.KindBranch) {
		if it.Meta[git.MetaWhy] == git.WhyOnly && !g.chosen(it) {
			only++
		}
	}
	fg := t.Muted
	if only > 0 {
		parts = append(parts, plural(only, "branch", "branches")+" only here · kept")
		fg = t.Danger
	}
	switch {
	case g.asking[repo.Path]:
		parts = append(parts, "asking origin…")
	case g.head(repo.Path).Meta[git.MetaRemote] == git.RemoteUnreachable:
		parts = append(parts, "origin did not answer")
	default:
		if n := g.taken(repo.Path, git.KindRemote); n > 0 && g.prune {
			parts = append(parts, humanize.Plural(n, "stale origin ref"))
		}
	}
	if repo.LFSBytes > 0 {
		parts = append(parts, "LFS cache "+humanize.Bytes(repo.LFSBytes))
	}
	return strings.Join(parts, " · "), fg
}

// totals sums up what the plan for every repository would take.
func (g *Git) totals() (repos, branches int, bytes int64) {
	for _, repo := range g.repos {
		if !g.chosen(g.head(repo.Path)) {
			continue
		}
		repos++
		branches += g.taken(repo.Path, git.KindBranch)
		if g.pack {
			bytes += g.reclaim(repo.Path)
		}
	}
	return repos, branches, bytes
}

// plural is a count with a noun whose plural is not just an added s.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return humanize.Count(int64(n)) + " " + many
}
