//go:build darwin

package screens

import (
	"fmt"
	"image/color"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/romus/space/internal/engine"
	"github.com/romus/space/internal/humanize"
	"github.com/romus/space/internal/recipes/agents"
	"github.com/romus/space/internal/ui/kit"
	"github.com/romus/space/pkg/recipe"
)

// project is one row of the AI agents screen.
type project struct {
	key      string
	title    string
	sessions []recipe.Item
	remove   int
	reclaim  int64
	last     time.Time
	pinned   bool
	active   bool
}

// Agents shows Claude Code and Codex sessions, grouped by project, with a
// keep window that decides what is old enough to go.
type Agents struct {
	c        *Ctx
	states   []*RecipeState
	tab      int
	keep     int // index into agents.KeepWindows
	cur, off int
	excluded []map[string]bool // per tab: projects the user unchecked
	drill    bool              // showing the sessions of one project
	dcur     int
	doff     int
	toast    toast
}

// NewAgents opens the screen on the recipe with the given id.
func NewAgents(c *Ctx, states []*RecipeState, id string) *Agents {
	a := &Agents{c: c, states: states}
	for i, rs := range states {
		a.excluded = append(a.excluded, map[string]bool{})
		if rs.R.ID() == id {
			a.tab = i
		}
	}
	days := c.State.KeepDays
	if days <= 0 {
		days = agents.DefaultKeepDays
	}
	for i, d := range agents.KeepWindows {
		if d <= days {
			a.keep = i
		}
	}
	return a
}

func (a *Agents) Title() string { return a.states[a.tab].R.ID() }
func (a *Agents) Typing() bool  { return false }

func (a *Agents) rs() *RecipeState        { return a.states[a.tab] }
func (a *Agents) src() agents.Source      { s, _ := a.rs().R.(agents.Source); return s }
func (a *Agents) days() int               { return agents.KeepWindows[a.keep] }
func (a *Agents) pinned() map[string]bool { return a.c.State.PinnedSet(a.rs().R.ID()) }

// selection is what would be removed with the current window and pins.
func (a *Agents) selection() []recipe.Item {
	return agents.Select(a.rs().Items, a.days(), a.pinned(), a.excluded[a.tab], a.c.Now())
}

func (a *Agents) projects() []project {
	pinned := a.pinned()
	removing := map[string]bool{}
	for _, it := range a.selection() {
		removing[it.Group+"/"+it.ID] = true
	}
	// Ignore the user's unchecks when counting, so a row keeps its numbers.
	would := agents.Select(a.rs().Items, a.days(), pinned, nil, a.c.Now())
	wouldSet := map[string]bool{}
	for _, it := range would {
		wouldSet[it.Group+"/"+it.ID] = true
	}

	by := map[string]*project{}
	var order []*project
	for _, it := range a.rs().Items {
		if agents.IsExtra(it.Group) {
			continue
		}
		p := by[it.Group]
		if p == nil {
			p = &project{key: it.Group, title: it.Title, pinned: pinned[it.Group]}
			by[it.Group] = p
			order = append(order, p)
		}
		p.sessions = append(p.sessions, it)
		if it.ModTime.After(p.last) {
			p.last = it.ModTime
		}
		if it.Locked != "" {
			p.active = true
		}
		if wouldSet[it.Group+"/"+it.ID] {
			p.remove++
			p.reclaim += it.Bytes
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].reclaim != order[j].reclaim {
			return order[i].reclaim > order[j].reclaim
		}
		return len(order[i].sessions) > len(order[j].sessions)
	})
	out := make([]project, len(order))
	for i, p := range order {
		sort.SliceStable(p.sessions, func(x, y int) bool { return p.sessions[x].ModTime.After(p.sessions[y].ModTime) })
		out[i] = *p
	}
	return out
}

func (a *Agents) Update(msg tea.Msg) (Screen, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return a, nil
	}
	k := keyOf(key)
	projects := a.projects()
	a.cur = min(a.cur, max(0, len(projects)-1))

	if a.drill {
		n := 0
		if a.cur < len(projects) {
			n = len(projects[a.cur].sessions)
		}
		if move(&a.dcur, k, n, max(1, a.c.H-12)) {
			return a, nil
		}
		if k == "esc" || k == "s" || k == "q" || k == "backspace" {
			a.drill = false
		}
		return a, nil
	}

	if move(&a.cur, k, len(projects), max(1, a.c.H-16)) {
		return a, nil
	}
	switch k {
	case "esc", "q", "backspace":
		return a, pop()
	case "tab", "shift+tab":
		if len(a.states) > 1 {
			a.tab, a.cur, a.off = (a.tab+1)%len(a.states), 0, 0
		}
	case "left", "h":
		a.setKeep(a.keep - 1)
	case "right", "l":
		a.setKeep(a.keep + 1)
	case "space":
		if a.cur < len(projects) {
			p := projects[a.cur]
			if p.pinned {
				a.toast.set("Pinned projects are kept whole — press p to unpin")
				break
			}
			ex := a.excluded[a.tab]
			ex[p.key] = !ex[p.key]
			a.cur = min(a.cur+1, len(projects)-1)
		}
	case "p", "*", "★":
		if a.cur < len(projects) {
			p := projects[a.cur]
			a.c.State.SetPinned(a.rs().R.ID(), p.key, !p.pinned)
			_ = a.c.State.Save()
		}
	case "s":
		if a.cur < len(projects) {
			a.drill, a.dcur, a.doff = true, 0, 0
		}
	case "enter":
		return a, a.plan()
	}
	return a, nil
}

func (a *Agents) setKeep(i int) {
	a.keep = min(max(i, 0), len(agents.KeepWindows)-1)
	a.c.State.KeepDays = a.days()
	_ = a.c.State.Save()
}

func (a *Agents) plan() tea.Cmd {
	sel := a.selection()
	opt := recipe.Options{KeepDays: a.days(), Pinned: a.pinned(), Toggles: map[string]bool{}}
	plan, err := a.rs().R.Plan(sel, opt)
	if err != nil {
		a.toast.set(err.Error())
		return nil
	}
	engine.Prepare(&plan)
	return push(NewPlan(a.c, PlanSpec{
		Plan:    plan,
		Crumbs:  []string{"Recipes", a.rs().R.Info().Name},
		Options: opt,
	}))
}

func keepLabel(days int) string {
	if days == 1 {
		return "last day"
	}
	return fmt.Sprintf("last %d days", days)
}

func (a *Agents) Lines() []string {
	c, t := a.c, a.c.Theme
	w := c.W
	muted := t.S(t.Muted)
	src := a.src()
	now := c.Now()

	// Header with one tab per agent.
	var right []kit.Span
	for i, rs := range a.states {
		var total int64
		for _, it := range rs.Items {
			total += it.Bytes
		}
		label := fmt.Sprintf(" %s %s ", rs.R.Info().Name, humanize.Bytes(total))
		st := muted
		if i == a.tab {
			st = t.S(t.Strong).Bold(true).Underline(true)
		}
		right = append(right, kit.Sp(t.Base(), " "), kit.Sp(st, strings.TrimSpace(label)), kit.Sp(t.Base(), " "))
	}
	top := []string{
		header(c, []kit.Span{kit.Sp(muted, "Recipes ›"), kit.Sp(t.S(t.Recipe), " ◆ AI agents")}, right),
		kit.Blank(t, w),
	}

	// The keep window.
	left := []kit.Span{
		kit.Sp(muted, "keep  "), kit.Sp(t.S(t.Faint), "◂ "),
		kit.Sp(t.S(t.Strong), keepLabel(a.days())), kit.Sp(t.S(t.Faint), " ▸  ·  "),
		kit.Sp(muted, "plus every project marked "), kit.Sp(t.S(t.Text), "★"),
	}
	var status []kit.Span
	if src != nil && src.Running() {
		status = []kit.Span{kit.Sp(t.S(t.Safe), "● "+src.BinaryName()+" is running · active sessions are safe")}
	}
	top = append(top, kit.Box(t, w, []string{kit.Row(w-4, t.Base(), left, status)})...)

	projects := a.projects()
	a.cur = min(a.cur, max(0, len(projects)-1))
	if a.drill && a.cur < len(projects) {
		return a.sessionLines(top, projects[a.cur])
	}

	nameW := max(16, w-(4+10+11+10+13))
	faint := t.S(t.Faint)
	top = append(top, kit.Row(w, t.Base(), []kit.Span{
		kit.Cell(faint, "", 4), kit.Cell(faint, "PROJECT", nameW), kit.CellR(faint, "SESSIONS", 10),
		kit.CellR(faint, "TO REMOVE", 11), kit.CellR(faint, "RECLAIM", 10), kit.CellR(faint, "LAST USED", 13),
	}, nil))

	visible := max(1, c.H-len(top)-6)
	a.off = kit.Window(a.cur, a.off, len(projects), visible)
	ex := a.excluded[a.tab]
	for i := a.off; i < len(projects) && i < a.off+visible; i++ {
		p := projects[i]
		cur := i == a.cur
		st := func(fg color.Color) lipgloss.Style {
			if cur {
				return t.Sel(fg)
			}
			return t.S(fg)
		}
		box, bc, nc := "[✓]", t.Strong, t.Text
		remove, gain := humanize.Count(int64(p.remove)), humanize.Bytes(p.reclaim)
		switch {
		case p.pinned:
			box, bc, nc, remove, gain = " ★ ", t.Text, t.Muted, "0", "—"
		case ex[p.key]:
			box, bc, nc, remove, gain = "[ ]", t.Muted, t.Muted, "0", "—"
		case p.remove == 0:
			box, bc, gain = "[ ]", t.Faint, "—"
		}
		if cur && !p.pinned && !ex[p.key] {
			nc = t.Strong
		}
		last := humanize.Age(p.last, now)
		if p.active {
			last = "now"
		}
		top = append(top, kit.Row(w, st(t.Text), []kit.Span{
			kit.Cell(st(bc), box, 4), kit.Cell(st(nc), c.Tilde(p.title), nameW),
			kit.CellR(st(t.Muted), humanize.Count(int64(len(p.sessions))), 10),
			kit.CellR(st(nc), remove, 11), kit.CellR(st(nc), gain, 10), kit.CellR(st(t.Muted), last, 13),
		}, nil))
	}
	if len(projects) == 0 {
		top = append(top, kit.Blank(t, w), kit.Row(w, t.Base(), []kit.Span{kit.Sp(muted, "  No sessions found.")}, nil))
	}

	// Leftovers and what is never touched.
	sel := a.selection()
	extra := map[string]int64{}
	var names []string
	var sessions int
	for _, it := range sel {
		if agents.IsExtra(it.Group) {
			name := strings.TrimPrefix(it.Group, agents.ExtraPrefix) + "/"
			if _, ok := extra[name]; !ok {
				names = append(names, name)
			}
			extra[name] += it.Bytes
		} else {
			sessions++
		}
	}
	sort.Strings(names)
	var also []string
	for _, n := range names {
		also = append(also, n+" "+humanize.Bytes(extra[n]))
	}
	alsoText := "nothing older than the keep window"
	if len(also) > 0 {
		alsoText = strings.Join(also, " · ")
	}
	never := ""
	if src != nil {
		never = "○ " + strings.Join(src.NeverTouch(), " · ")
	}
	bottom := []string{kit.Rule(t, w)}
	if msg := a.toast.get(); msg != "" {
		bottom = append(bottom, kit.Row(w, t.Base(), []kit.Span{kit.Sp(t.S(t.Strong), msg)}, nil), kit.Blank(t, w))
	} else {
		bottom = append(bottom,
			kit.Row(w, t.Base(), []kit.Span{kit.Cell(muted, "also clean", 14), kit.Sp(t.S(t.Text), alsoText)}, nil),
			kit.Row(w, t.Base(), []kit.Span{kit.Cell(muted, "never touch", 14), kit.Sp(muted, never)}, nil))
	}

	hints := []kit.Hint{{"←→", "keep window"}, {"p", "pin project ★"}, {"␣", "skip"}, {"s", "sessions"}}
	if len(a.states) > 1 {
		hints = append(hints, kit.Hint{Key: "tab", Label: a.states[(a.tab+1)%len(a.states)].R.Info().Name})
	}
	sum := []kit.Span{
		kit.Sp(muted, humanize.Plural(sessions, "session")+" · "),
		kit.Sp(t.S(t.Strong), humanize.Bytes(recipe.Sum(sel))),
	}
	bottom = append(bottom, kit.Blank(t, w), kit.Footer(t, w, hints, sum, "⏎", "plan"))
	return kit.Frame(t, w, c.H, top, bottom)
}

// sessionLines lists the sessions of one project, newest first.
func (a *Agents) sessionLines(top []string, p project) []string {
	c, t := a.c, a.c.Theme
	w := c.W
	muted := t.S(t.Muted)
	now := c.Now()
	removing := map[string]bool{}
	for _, it := range a.selection() {
		removing[it.ID] = true
	}

	top = append(top, kit.Row(w, t.Base(), []kit.Span{
		kit.Sp(t.S(t.Strong), c.Tilde(p.title)),
		kit.Sp(muted, "  "+humanize.Plural(len(p.sessions), "session")),
	}, nil))
	idW := max(16, w-(4+12+16+14))
	faint := t.S(t.Faint)
	top = append(top, kit.Row(w, t.Base(), []kit.Span{
		kit.Cell(faint, "", 4), kit.Cell(faint, "SESSION", idW), kit.CellR(faint, "SIZE", 12),
		kit.CellR(faint, "LAST USED", 16), kit.CellR(faint, "", 14),
	}, nil))

	visible := max(1, c.H-len(top)-2)
	a.dcur = min(a.dcur, max(0, len(p.sessions)-1))
	a.doff = kit.Window(a.dcur, a.doff, len(p.sessions), visible)
	for i := a.doff; i < len(p.sessions) && i < a.doff+visible; i++ {
		it := p.sessions[i]
		cur := i == a.dcur
		st := func(fg color.Color) lipgloss.Style {
			if cur {
				return t.Sel(fg)
			}
			return t.S(fg)
		}
		mark, mc, verdict, vc := "·", t.Faint, "keep", t.Muted
		switch {
		case it.Locked != "":
			mark, mc, verdict, vc = "○", t.Safe, it.Locked, t.Safe
		case removing[it.ID]:
			mark, mc, verdict, vc = "✓", t.Strong, "remove", t.Text
		}
		top = append(top, kit.Row(w, st(t.Text), []kit.Span{
			kit.Cell(st(mc), " "+mark, 4), kit.Cell(st(t.Text), it.ID, idW),
			kit.CellR(st(t.Text), humanize.Bytes(it.Bytes), 12),
			kit.CellR(st(t.Muted), humanize.Ago(it.ModTime, now), 16), kit.CellR(st(vc), verdict, 14),
		}, nil))
	}
	bottom := []string{kit.Footer(t, w, []kit.Hint{{"↑↓", "move"}, {"esc", "back to projects"}}, nil, "", "")}
	return kit.Frame(t, w, c.H, top, bottom)
}
