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
	"github.com/romus/space/internal/ui/kit"
	"github.com/romus/space/pkg/recipe"
)

// PlanSpec is everything the plan, run and done screens need.
type PlanSpec struct {
	Plan   recipe.Plan
	Crumbs []string // "Recipes", "Podman"
	// Rebuild makes the plan again after a toggle changed. Nil when the
	// plan has no toggles.
	Rebuild func(opt recipe.Options) (recipe.Plan, error)
	Options recipe.Options
	// Measure reads the recipe's own number before, during and after.
	Measure func(ctx context.Context) (string, int64, bool)
}

func (s PlanSpec) crumbs(c *Ctx, last string) []kit.Span {
	t := c.Theme
	path := append(append([]string{}, s.Crumbs...), "")
	return []kit.Span{
		kit.Sp(t.S(t.Muted), strings.Join(path, " › ")),
		kit.Sp(t.S(t.Strong), last),
	}
}

// Plan shows exactly what will run, before anything runs.
type Plan struct {
	c        *Ctx
	spec     PlanSpec
	cur, off int
	confirm  bool
	toast    toast

	editing bool
	input   string
	edited  map[int]bool

	fold *planFold // nil for a plan without groups
}

// NewPlan opens the plan screen.
func NewPlan(c *Ctx, spec PlanSpec) *Plan {
	if spec.Options.Toggles == nil {
		spec.Options.Toggles = map[string]bool{}
	}
	return &Plan{c: c, spec: spec, edited: map[int]bool{}, fold: newFold(spec.Plan)}
}

func (p *Plan) Title() string { return strings.ToLower(p.spec.Plan.Title) + " — plan" }
func (p *Plan) Typing() bool  { return p.editing }

func (p *Plan) Update(msg tea.Msg) (Screen, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return p, nil
	}
	k := keyOf(key)
	steps := p.spec.Plan.Steps

	if p.editing {
		switch k {
		case "esc":
			p.editing = false
		case "enter":
			p.editing = false
			argv := splitCommand(p.input)
			if len(argv) == 0 {
				p.toast.set("An empty command — the step is unchanged")
				break
			}
			p.spec.Plan.Steps[p.cur].Cmd = argv
			p.spec.Plan.Steps[p.cur].Sudo = false
			p.edited[p.cur] = true
			p.spec.Rebuild = nil // an edited plan is no longer the recipe's
		case "backspace":
			if r := []rune(p.input); len(r) > 0 {
				p.input = string(r[:len(r)-1])
			}
		default:
			if key.Text != "" {
				p.input += key.Text
			}
		}
		return p, nil
	}

	if k != "enter" {
		p.confirm = false
	}
	if p.fold != nil && p.foldKey(k) {
		return p, nil
	}
	switch k {
	case "up", "k":
		p.cur = max(0, p.cur-1)
	case "down", "j":
		p.cur = min(len(steps)-1, p.cur+1)
	case "esc", "left", "h", "q":
		return p, pop()
	case "c":
		p.toast.set("Copied as a shell script")
		return p, copyText(engine.Script(p.spec.Plan, p.c.Now()))
	case "e":
		if p.cur >= len(steps) {
			break
		}
		if steps[p.cur].Remove != nil {
			p.toast.set("This step is a list of files. Go back and change the marks instead.")
			break
		}
		p.editing = true
		p.input = engine.Display(steps[p.cur], "")
	case "1", "2", "3", "4":
		i := int(k[0] - '1')
		if i < len(p.spec.Plan.Toggles) && p.spec.Rebuild != nil {
			tg := p.spec.Plan.Toggles[i]
			p.spec.Options.Toggles[tg.Key] = !tg.On
			plan, err := p.spec.Rebuild(p.spec.Options)
			if err != nil {
				p.toast.set(err.Error())
				break
			}
			engine.Prepare(&plan)
			p.spec.Plan = plan
			p.cur = min(p.cur, len(plan.Steps)-1)
			p.fold = newFold(plan)
		}
	case "enter":
		if len(steps) == 0 {
			break
		}
		if p.irreversible() && !p.confirm {
			p.confirm = true
			break
		}
		return p, runCmd(p.c, p.spec)
	}
	return p, nil
}

func (p *Plan) irreversible() bool {
	for _, s := range p.spec.Plan.Steps {
		if s.Irreversible() {
			return true
		}
	}
	return false
}

func (p *Plan) Lines() []string {
	if p.fold != nil && !p.fold.detail {
		return p.foldLines()
	}
	c, t := p.c, p.c.Theme
	w := c.W
	plan := p.spec.Plan

	sub := fmt.Sprintf("%s · ≈ %s · nothing has run yet",
		humanize.Plural(len(plan.Steps), "step"), humanize.Duration(secs(plan.Seconds)))
	title := "Here’s what will happen"
	hints := []kit.Hint{{"e", "edit step"}, {"c", "copy as script"}, {"esc", "back"}}
	if p.fold != nil {
		title, sub = p.fold.where(plan, p.cur)
		hints = []kit.Hint{{"tab", "next " + plan.Noun()}, {"e", "edit step"}, {"esc", "all " + plan.Noun() + "s"}}
	}
	top := []string{
		header(c, p.spec.crumbs(c, "Plan"), nil),
		kit.Blank(t, w),
		kit.Row(w, t.Base(), []kit.Span{
			kit.Sp(t.S(t.Strong).Bold(true), title),
			kit.Sp(t.S(t.Muted), "  "+sub),
		}, nil),
		kit.Blank(t, w),
	}

	visible := max(1, (c.H-len(top)-4)/4)
	p.off = kit.Window(p.cur, p.off, len(plan.Steps), visible)
	for i := p.off; i < len(plan.Steps) && i < p.off+visible; i++ {
		top = append(top, p.stepRows(i)...)
	}
	if n := len(plan.Steps) - p.off - visible; n > 0 {
		top[len(top)-1] = kit.Row(w, t.Base(), []kit.Span{
			kit.Sp(t.S(t.Faint), fmt.Sprintf("     ↓ %s more", humanize.Plural(n, "step"))),
		}, nil)
	}
	return kit.Frame(t, w, c.H, top, p.bottom(hints, p.action()))
}

// stepRows draws one step: its title and tag, its command, why, and a gap.
func (p *Plan) stepRows(i int) []string {
	c, t := p.c, p.c.Theme
	w := c.W
	s := p.spec.Plan.Steps[i]
	cur := i == p.cur
	st := func(fg color.Color) lipgloss.Style {
		if cur {
			return t.Soft(fg)
		}
		return t.S(fg)
	}
	tag := s.Tag()
	if p.edited[i] {
		tag = "EDITED · " + tag
	}
	cmd := "$ " + engine.Display(s, c.Home())
	cmdStyle := st(t.Subtle)
	if cur && p.editing {
		cmd = "$ " + p.input + "▍"
		cmdStyle = st(t.Strong)
	}
	return []string{
		kit.Row(w, st(t.Text), []kit.Span{
			kit.CellR(st(t.Faint), fmt.Sprint(i+1), 3), kit.Cell(st(t.Text), "", 2),
			kit.Sp(st(t.Strong), s.Title),
		}, []kit.Span{kit.Sp(st(tagColor(c, s)), tag+" ")}),
		kit.Row(w, st(t.Text), []kit.Span{kit.Cell(st(t.Text), "", 5), kit.Sp(cmdStyle, kit.Fit(cmd, w-6))}, nil),
		kit.Row(w, st(t.Text), []kit.Span{kit.Cell(st(t.Text), "", 5), kit.Sp(st(t.Muted), s.Why)}, nil),
		kit.Blank(t, w),
	}
}

// action is what enter does, in words.
func (p *Plan) action() string {
	plan := p.spec.Plan
	switch {
	case plan.Reclaim > 0:
		return "run and reclaim ≈ " + humanize.Bytes(plan.Reclaim)
	case plan.ToTrash > 0:
		return "run · move " + humanize.Bytes(plan.ToTrash) + " to the Trash"
	}
	return "run"
}

// bottom is what changes, the switches, and the keys.
func (p *Plan) bottom(hints []kit.Hint, action string) []string {
	c, t := p.c, p.c.Theme
	w := c.W
	plan := p.spec.Plan
	muted := t.S(t.Muted)
	var left []kit.Span
	switch {
	case plan.Metric != nil:
		left = []kit.Span{
			kit.Sp(muted, plan.Metric.Label+"   "), kit.Sp(t.S(t.Text), humanize.Bytes(plan.Metric.Before)),
			kit.Sp(t.S(t.Faint), "   →   "), kit.Sp(t.S(t.Strong).Bold(true), "≈ "+humanize.Bytes(plan.Metric.After)),
		}
	case plan.ToTrash > 0 && plan.Reclaim == 0:
		left = []kit.Span{
			kit.Sp(muted, "moves to the Trash   "), kit.Sp(t.S(t.Strong).Bold(true), humanize.Bytes(plan.ToTrash)),
			kit.Sp(muted, "   space comes back when the Trash is emptied"),
		}
	case plan.Reclaim > 0:
		left = []kit.Span{kit.Sp(muted, "frees   "), kit.Sp(t.S(t.Strong).Bold(true), "≈ "+humanize.Bytes(plan.Reclaim))}
		if plan.ToTrash > 0 {
			left = append(left, kit.Sp(muted, "   plus "+humanize.Bytes(plan.ToTrash)+" to the Trash"))
		}
	}
	var right []kit.Span
	if p.spec.Rebuild != nil {
		for i, tg := range plan.Toggles {
			box, bc := "[ ]", t.Muted
			if tg.On {
				box, bc = "[✓]", t.Strong
			}
			right = append(right, kit.Sp(t.S(t.Text), fmt.Sprintf("   %d ", i+1)), kit.Sp(t.S(bc), box), kit.Sp(muted, " "+tg.Label))
		}
	}
	bottom := []string{kit.Rule(t, w)}
	if msg := p.toast.get(); msg != "" {
		bottom = append(bottom, kit.Row(w, t.Base(), []kit.Span{kit.Sp(t.S(t.Strong), msg)}, nil))
	} else {
		bottom = append(bottom, kit.Row(w, t.Base(), left, right))
	}
	var sum []kit.Span
	switch {
	case p.editing:
		hints = []kit.Hint{{"enter", "save"}, {"esc", "cancel"}}
		action = ""
	case p.confirm:
		sum = []kit.Span{kit.Sp(t.S(t.Danger), "this cannot be undone — press enter again")}
	}
	return append(bottom, kit.Blank(t, w), kit.Footer(t, w, hints, sum, "⏎", action))
}
