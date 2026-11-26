//go:build darwin

package screens

import (
	"context"
	"fmt"
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/romus/space/internal/engine"
	"github.com/romus/space/internal/humanize"
	"github.com/romus/space/internal/recipes/podman"
	"github.com/romus/space/internal/ui/kit"
	"github.com/romus/space/pkg/recipe"
)

var podmanSections = []struct{ group, label, column, noun string }{
	{podman.Images, "Images", "IMAGE", "image"},
	{podman.Containers, "Containers", "CONTAINER", "container"},
	{podman.Volumes, "Volumes", "VOLUME", "volume"},
	{podman.BuildCache, "Build cache", "CACHE", "cache"},
}

// Podman lets the user pick images, containers and volumes to remove.
type Podman struct {
	c        *Ctx
	rs       *RecipeState
	rec      *podman.Recipe
	section  int
	cur, off int
	sel      map[string]bool
	toast    toast
}

// NewPodman opens the Podman recipe.
func NewPodman(c *Ctx, rs *RecipeState) *Podman {
	p := &Podman{c: c, rs: rs, sel: map[string]bool{}}
	p.rec, _ = rs.R.(*podman.Recipe)
	for _, it := range recipe.DefaultSelection(rs.Items) {
		p.sel[it.Group+"/"+it.ID] = true
	}
	return p
}

func (p *Podman) Title() string { return "podman" }
func (p *Podman) Typing() bool  { return false }

func (p *Podman) items(group string) []recipe.Item {
	var out []recipe.Item
	for _, it := range p.rs.Items {
		if it.Group == group {
			out = append(out, it)
		}
	}
	return out
}

func (p *Podman) selected() []recipe.Item {
	var out []recipe.Item
	for _, it := range p.rs.Items {
		if p.sel[it.Group+"/"+it.ID] && it.Locked == "" {
			out = append(out, it)
		}
	}
	return out
}

func (p *Podman) Update(msg tea.Msg) (Screen, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return p, nil
	}
	k := keyOf(key)
	rows := p.items(podmanSections[p.section].group)
	if move(&p.cur, k, len(rows), max(1, p.c.H-16)) {
		return p, nil
	}
	switch k {
	case "esc", "left", "h", "q":
		return p, pop()
	case "tab", "right", "l":
		p.section, p.cur, p.off = (p.section+1)%len(podmanSections), 0, 0
	case "shift+tab":
		p.section, p.cur, p.off = (p.section+len(podmanSections)-1)%len(podmanSections), 0, 0
	case "space":
		if p.cur < len(rows) {
			it := rows[p.cur]
			if it.Locked != "" {
				p.toast.set("In use by " + it.Locked + " — it stays")
				break
			}
			id := it.Group + "/" + it.ID
			p.sel[id] = !p.sel[id]
			p.cur = min(p.cur+1, len(rows)-1)
		}
	case "a":
		// Everything nothing is using, in this section.
		for _, it := range rows {
			if it.Locked == "" && it.Meta["containers"] == "" && it.Group != podman.Volumes {
				p.sel[it.Group+"/"+it.ID] = true
			}
		}
	case "n":
		for _, it := range rows {
			delete(p.sel, it.Group+"/"+it.ID)
		}
	case "enter":
		return p, p.plan()
	}
	return p, nil
}

func (p *Podman) plan() tea.Cmd {
	opt := recipe.Options{Toggles: map[string]bool{}}
	sel := p.selected()
	plan, err := p.rs.R.Plan(sel, opt)
	if err != nil {
		p.toast.set(err.Error())
		return nil
	}
	engine.Prepare(&plan)
	spec := PlanSpec{
		Plan:    plan,
		Crumbs:  []string{"Recipes", "Podman"},
		Options: opt,
		Rebuild: func(o recipe.Options) (recipe.Plan, error) { return p.rs.R.Plan(sel, o) },
	}
	if m, ok := p.rs.R.(recipe.Measurer); ok {
		spec.Measure = func(ctx context.Context) (string, int64, bool) { return m.Measure(ctx) }
	}
	return push(NewPlan(p.c, spec))
}

func (p *Podman) Lines() []string {
	c, t := p.c, p.c.Theme
	w := c.W
	muted := t.S(t.Muted)
	var st podman.Status
	if p.rec != nil {
		st = p.rec.Status()
	}

	version := "podman"
	if st.Version != "" {
		version += " " + st.Version
	}
	if st.VMType != "" {
		version += " · " + st.VMType
	}
	top := []string{
		header(c, []kit.Span{kit.Sp(muted, "Recipes ›"), kit.Sp(t.S(t.Recipe), " ◆ Podman")},
			[]kit.Span{kit.Sp(muted, version)}),
		kit.Blank(t, w),
	}

	// The machine.
	dot, state := t.Faint, "stopped"
	if st.Running {
		dot, state = t.Safe, "running"
	}
	left := []kit.Span{
		kit.Sp(t.S(dot), "● "), kit.Sp(t.S(t.Strong), st.Machine), kit.Sp(muted, "   "+state),
	}
	if st.CPUs > 0 {
		left = append(left, kit.Sp(muted, fmt.Sprintf("   %d CPU · %s", st.CPUs, humanize.Binary(st.Memory))))
	}
	right := []kit.Span{
		kit.Sp(muted, "disk on Mac  "), kit.Sp(t.S(t.Strong), humanize.Bytes(st.OnDisk)),
	}
	if st.DiskSize > 0 {
		right = append(right, kit.Sp(t.S(t.Faint), " of "+humanize.Binary(st.DiskSize)))
	}
	top = append(top, kit.Box(t, w, []string{kit.Row(w-4, t.Base(), left, right)})...)

	if !st.Running {
		top = append(top, kit.Blank(t, w),
			kit.Row(w, t.Base(), []kit.Span{kit.Sp(t.S(t.Text), "  The machine is stopped.")}, nil),
			kit.Row(w, t.Base(), []kit.Span{
				kit.Sp(muted, "  Images and containers can only be listed, removed and trimmed while it runs."),
			}, nil))
		bottom := []string{kit.Footer(t, w, []kit.Hint{{"esc", "back"}}, nil, "⏎", "plan: start the machine")}
		return kit.Frame(t, w, c.H, top, bottom)
	}

	// Sections.
	var tabs []kit.Tab
	for _, s := range podmanSections {
		items := p.items(s.group)
		badge := fmt.Sprint(len(items))
		if s.group == podman.BuildCache {
			badge = ""
			if n := recipe.Sum(items); n > 0 {
				badge = humanize.Bytes(n)
			}
		}
		tabs = append(tabs, kit.Tab{Label: s.label, Badge: badge})
	}
	line, rule := kit.Tabs(t, w, tabs, p.section, nil)
	top = append(top, line, rule)

	sec := podmanSections[p.section]
	rows := p.items(sec.group)
	usedW := max(0, min(26, w-(4+13+10+10+3)-28))
	nameW := max(16, w-(4+13+10+10+3)-usedW)
	faint := t.S(t.Faint)
	top = append(top, kit.Row(w, t.Base(), []kit.Span{
		kit.Cell(faint, "", 4), kit.Cell(faint, sec.column, nameW), kit.Cell(faint, "ID", 13),
		kit.CellR(faint, "SIZE", 10), kit.CellR(faint, "CREATED", 10),
		kit.Cell(faint, "", 3), kit.Cell(faint, "USED BY", usedW),
	}, nil))

	visible := max(1, c.H-len(top)-5)
	p.off = kit.Window(p.cur, p.off, len(rows), visible)
	now := c.Now()
	for i := p.off; i < len(rows) && i < p.off+visible; i++ {
		it := rows[i]
		cur := i == p.cur
		style := func(fg color.Color) lipgloss.Style {
			if cur {
				return t.Sel(fg)
			}
			return t.S(fg)
		}
		box, bc, nc, uc := "[ ]", t.Muted, t.Text, t.Muted
		used := it.Meta["used"]
		switch {
		case it.Locked != "":
			box, bc, nc, uc, used = " ○ ", t.Faint, t.Muted, t.Safe, it.Locked
		case p.sel[it.Group+"/"+it.ID]:
			box, bc = "[✓]", t.Strong
		}
		if cur && it.Locked == "" {
			nc = t.Strong
		}
		if used == "" {
			used = "—"
		}
		// An image is one row whatever its number of tags: removing it
		// drops them all, so the row says how many more there are.
		name := it.Title
		if n := strings.Count(it.Meta["tags"], ","); n > 0 {
			more := "  +" + humanize.Plural(n, "tag")
			name = strings.TrimRight(kit.Fit(name, max(1, nameW-1-kit.Width(more))), " ") + more
		}
		created := "—"
		if !it.ModTime.IsZero() && it.ModTime.Unix() > 0 {
			created = humanize.Age(it.ModTime, now)
		}
		top = append(top, kit.Row(w, style(t.Text), []kit.Span{
			kit.Cell(style(bc), box, 4), kit.Cell(style(nc), name, nameW),
			kit.Cell(style(t.Faint), it.Meta["id"], 13),
			kit.CellR(style(nc), humanize.Bytes(it.Bytes), 10), kit.CellR(style(t.Muted), created, 10),
			kit.Cell(style(t.Text), "", 3), kit.Cell(style(uc), used, usedW),
		}, nil))
	}
	if len(rows) == 0 {
		top = append(top, kit.Blank(t, w), kit.Row(w, t.Base(), []kit.Span{kit.Sp(muted, "  Nothing here.")}, nil))
	}

	sel := p.selected()
	freed := recipe.Sum(sel)
	bottom := []string{kit.Rule(t, w)}
	if msg := p.toast.get(); msg != "" {
		bottom = append(bottom, kit.Row(w, t.Base(), []kit.Span{kit.Sp(t.S(t.Strong), msg)}, nil), kit.Blank(t, w))
	} else {
		bottom = append(bottom,
			kit.Row(w, t.Base(), []kit.Span{
				kit.Sp(t.S(t.Strong), humanize.Bytes(freed)),
				kit.Sp(muted, " will be freed inside the machine. On your Mac — only after "),
				kit.Sp(t.S(t.Text), "fstrim"), kit.Sp(muted, ":"),
			}, nil),
			kit.Row(w, t.Base(), []kit.Span{
				kit.Sp(muted, "the machine’s disk is sparse, and macOS won’t know blocks are free until Linux tells it. The plan does this for you."),
			}, nil))
	}
	sum := []kit.Span{
		kit.Sp(muted, fmt.Sprintf("%s · ", humanize.Plural(len(sel), "item"))),
		kit.Sp(t.S(t.Strong), humanize.Bytes(freed)),
	}
	bottom = append(bottom, kit.Blank(t, w), kit.Footer(t, w,
		[]kit.Hint{{"␣", "mark"}, {"a", "all unused"}, {"n", "none"}, {"tab", "section"}, {"esc", "back"}},
		sum, "⏎", "plan"))
	return kit.Frame(t, w, c.H, top, bottom)
}
