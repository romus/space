//go:build darwin

package screens

import (
	tea "charm.land/bubbletea/v2"

	"github.com/romus/space/internal/recipes/git"
)

func (g *Git) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case gitRemoteMsg:
		g.running--
		delete(g.asking, msg.repo)
		g.rs.Items = git.ApplyRemote(g.rs.Items, msg.repo, msg.have, msg.err)
		if msg.err == nil {
			g.estimate(msg.repo) // stale refs are chosen now
		}
		return g, g.pump()
	case gitEstimateMsg:
		g.running--
		if msg.gen == g.gen[msg.repo] {
			g.est[msg.repo] = msg.bytes
		}
		return g, g.pump()
	case tea.KeyPressMsg:
		k := keyOf(msg)
		switch {
		case g.editing:
			g.ruleKey(k, msg)
		case g.open >= 0:
			return g, g.repoKey(k)
		default:
			return g, g.listKey(k)
		}
		return g, g.pump()
	}
	return g, nil
}

// listKey handles the keys of the list of repositories.
func (g *Git) listKey(k string) tea.Cmd {
	if move(&g.cur, k, len(g.repos), max(1, g.c.H-16)) {
		return nil
	}
	switch k {
	case "esc", "left", "h", "q", "backspace":
		return pop()
	case "space":
		repo := g.repos[g.cur]
		head := g.head(repo.Path)
		if head.Locked != "" {
			g.toast.set(repo.Name + ": " + head.Locked + " — it is skipped")
			break
		}
		g.marks[git.MarkKey(head)] = !g.chosen(head)
		g.cur = min(g.cur+1, len(g.repos)-1)
	case "right", "l":
		g.open, g.tab, g.rcur, g.roff = g.cur, gitLocal, 0, 0
	case "e":
		g.startRule()
	case "p":
		g.prune = !g.prune
	case "c":
		g.pack = !g.pack
	case "b":
		g.save = !g.save
	case "enter":
		return g.plan("")
	}
	return nil
}

// repoKey handles the keys of one repository.
func (g *Git) repoKey(k string) tea.Cmd {
	rows := g.rows()
	if move(&g.rcur, k, len(rows), max(1, g.c.H-14)) {
		return nil
	}
	repo := g.repos[g.open]
	switch k {
	case "esc", "q", "backspace":
		return g.back()
	case "tab":
		g.tab, g.rcur, g.roff = (g.tab+1)%3, 0, 0
	case "shift+tab":
		g.tab, g.rcur, g.roff = (g.tab+2)%3, 0, 0
	case "space":
		if g.rcur < len(rows) {
			g.toggle(rows[g.rcur])
			g.rcur = min(g.rcur+1, len(rows)-1)
		}
	case "m":
		if repo.Skip != "" {
			g.toast.set(g.skipped(repo))
			break
		}
		// Merged only: the most careful choice there is.
		for _, it := range g.of(repo.Path, git.KindBranch) {
			if it.Locked == "" && !git.Kept(it.Title, g.keep) {
				g.marks[git.MarkKey(it)] = it.Meta[git.MetaWhy] == git.WhyMerged
			}
		}
		g.estimate(repo.Path)
		g.toast.set("Only the merged branches are marked")
	case "right", "l":
		if g.rcur < len(rows) && rows[g.rcur].fold != "" {
			it := rows[g.rcur].items[0]
			g.expanded[gitFoldKey(it, g.rank(it))] = true
		}
	case "left", "h":
		if g.rcur < len(rows) {
			it := rows[g.rcur].items[0]
			if key := gitFoldKey(it, g.rank(it)); g.expanded[key] {
				delete(g.expanded, key)
				g.rcur = g.rowOf(it.ID)
				break
			}
		}
		return g.back()
	case "enter":
		return g.plan(repo.Path)
	}
	return g.pump()
}

// back leaves a repository: to the list, or out when there is no list.
func (g *Git) back() tea.Cmd {
	if g.single {
		return pop()
	}
	g.open = -1
	return nil
}

// rowOf finds the row that holds an item, after folds changed.
func (g *Git) rowOf(id string) int {
	for n, row := range g.rows() {
		for _, it := range row.items {
			if it.ID == id {
				return n
			}
		}
	}
	return 0
}

// toggle marks or unmarks a row. What is locked, or kept by the rule, says
// why it cannot be marked.
func (g *Git) toggle(row gitRow) {
	if repo := g.repos[g.open]; repo.Skip != "" {
		g.toast.set(g.skipped(repo))
		return
	}
	all, _ := g.state(row)
	changed := false
	for _, it := range row.items {
		switch {
		case it.Locked != "":
			g.toast.set(it.Title + " stays: " + it.Locked)
		case it.Meta[git.MetaKind] == git.KindBranch && git.Kept(it.Title, g.keep):
			g.toast.set(it.Title + " is kept by the rule — change the rule with e on the list")
		default:
			g.marks[git.MarkKey(it)] = !all
			changed = true
		}
	}
	if changed {
		g.estimate(row.items[0].Group)
	}
}
