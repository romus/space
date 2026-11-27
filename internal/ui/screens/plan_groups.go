//go:build darwin

package screens

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/romus/space/internal/engine"
	"github.com/romus/space/internal/humanize"
	"github.com/romus/space/internal/ui/kit"
)

// foldKey handles the keys that mean something else on a plan with groups.
// It reports whether the key was used.
func (p *Plan) foldKey(k string) bool {
	f := p.fold
	if f.detail {
		switch k {
		case "esc", "left", "h", "q":
			f.detail = false
			f.cur = f.line(p.cur)
		case "tab":
			p.cur = f.jump(p.cur, 1)
		case "shift+tab":
			p.cur = f.jump(p.cur, -1)
		default:
			return false
		}
		return true
	}
	switch k {
	case "up", "k":
		f.cur = max(0, f.cur-1)
	case "down", "j":
		f.cur = min(len(f.lines)-1, f.cur+1)
	case "right", "l":
		f.detail = true
		p.cur = f.lines[f.cur].example()
	case "e":
		// A folded line stands for a different command in every group.
		p.toast.set("Open a " + p.spec.Plan.Noun() + " with → to edit its command")
	default:
		return false
	}
	return true
}

// foldLines draws the folded plan: each kind of step once, then which
// groups it runs in and what is left alone.
func (p *Plan) foldLines() []string {
	c, t := p.c, p.c.Theme
	w := c.W
	plan, f := p.spec.Plan, p.fold
	noun := plan.Noun()

	title, sub := f.headline(plan)
	top := []string{
		header(c, p.spec.crumbs(c, "Plan"), nil),
		kit.Blank(t, w),
		kit.Row(w, t.Base(), []kit.Span{
			kit.Sp(t.S(t.Strong).Bold(true), title),
			kit.Sp(t.S(t.Muted), "  "+sub),
		}, nil),
		kit.Blank(t, w),
	}
	info := p.foldInfo()

	const rows = 3
	visible := max(1, (c.H-len(top)-len(info)-4)/rows)
	f.cur = min(f.cur, len(f.lines)-1)
	f.off = kit.Window(f.cur, f.off, len(f.lines), visible)
	for i := f.off; i < len(f.lines) && i < f.off+visible; i++ {
		top = append(top, p.foldRows(i)...)
	}
	if n := len(f.lines) - f.off - visible; n > 0 {
		top[len(top)-1] = kit.Row(w, t.Base(), []kit.Span{
			kit.Sp(t.S(t.Faint), fmt.Sprintf("     ↓ %d more", n)),
		}, nil)
	}
	top = append(top, info...)

	action := "run in " + humanize.Plural(len(f.groups), noun)
	if plan.Reclaim > 0 {
		action += " · ≈ " + humanize.Bytes(plan.Reclaim)
	}
	hints := []kit.Hint{{"→", "per-" + noun + " detail"}, {"c", "copy as script"}, {"esc", "back"}}
	return kit.Frame(t, w, c.H, top, p.bottom(hints, action))
}

// foldRows draws one folded line: what it is and its tag, an example of the
// command, and, under the cursor, why.
func (p *Plan) foldRows(n int) []string {
	c, t := p.c, p.c.Theme
	w := c.W
	plan := p.spec.Plan
	l := p.fold.lines[n]
	cur := n == p.fold.cur
	st := func(fg color.Color) lipgloss.Style {
		if cur {
			return t.Soft(fg)
		}
		return t.S(fg)
	}
	worst, example := plan.Steps[l.worst], plan.Steps[l.example()]
	left := []kit.Span{
		kit.CellR(st(t.Faint), fmt.Sprint(n+1), 3), kit.Cell(st(t.Text), "", 2),
		kit.Sp(st(t.Strong), l.title),
	}
	if l.note != "" {
		left = append(left, kit.Sp(st(t.Muted), "  "+l.note))
	}
	why := kit.Blank(t, w)
	if cur {
		why = kit.Row(w, st(t.Text), []kit.Span{kit.Cell(st(t.Text), "", 5), kit.Sp(st(t.Muted), example.Why)}, nil)
	}
	cmd := "$ " + engine.Display(example, c.Home())
	return []string{
		kit.Row(w, st(t.Text), left, []kit.Span{kit.Sp(st(tagColor(c, worst)), worst.Tag()+" ")}),
		kit.Row(w, st(t.Text), []kit.Span{kit.Cell(st(t.Text), "", 5), kit.Sp(st(t.Subtle), kit.Fit(cmd, w-6))}, nil),
		why,
	}
}

// foldInfo is the box under a folded plan: the groups it runs in, and the
// recipe's notes about what is skipped and what is left alone.
func (p *Plan) foldInfo() []string {
	c, t := p.c, p.c.Theme
	w := c.W
	plan, f := p.spec.Plan, p.fold
	if w < 30 {
		return nil
	}
	const label = 11
	room := w - 4 - label
	row := func(name string, fg color.Color, value string) string {
		return kit.Row(w-4, t.Base(), []kit.Span{
			kit.Cell(t.S(t.Muted), name, label), kit.Sp(t.S(fg), kit.Fit(value, room)),
		}, nil)
	}
	names := make([]string, len(f.groups))
	for i, g := range f.groups {
		names[i] = g.Name
	}
	lines := []string{row("in", t.Text, joinFit(names, room))}
	for i, n := range plan.Notes {
		if i == 3 {
			break
		}
		name, value, _ := strings.Cut(n, "\t")
		lines = append(lines, row(name, t.Muted, value))
	}
	return kit.Box(t, w, lines)
}
