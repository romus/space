//go:build darwin

package screens

import (
	"fmt"
	"math"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/romus/space/internal/engine"
	"github.com/romus/space/internal/humanize"
	"github.com/romus/space/internal/recipes/trash"
	"github.com/romus/space/internal/scan"
	"github.com/romus/space/internal/ui/kit"
	"github.com/romus/space/pkg/recipe"
)

// noise is how much free space may drift on its own during a run.
const noise = 5e6

// Done shows what a run really achieved.
type Done struct {
	c       *Ctx
	spec    PlanSpec
	res     engine.Result
	vol     scan.Volume
	actions []recipe.Action
}

// NewDone builds the final screen from a result.
func NewDone(c *Ctx, spec PlanSpec, res engine.Result) *Done {
	d := &Done{c: c, spec: spec, res: res}
	d.vol, _ = scan.VolumeOf(c.Root)
	if res.Failed < 0 && !res.Stopped {
		d.actions = append(d.actions, spec.Plan.After...)
	}
	if res.Trashed > 0 {
		d.actions = append(d.actions, recipe.Action{Key: "e", Label: "empty Trash", Plan: trash.EmptyTrash()})
	}
	return d
}

func (d *Done) Title() string { return strings.ToLower(d.spec.Plan.Title) + " — done" }
func (d *Done) Typing() bool  { return false }

func (d *Done) Update(msg tea.Msg) (Screen, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return d, nil
	}
	k := keyOf(key)
	for _, a := range d.actions {
		if k == a.Key {
			plan := a.Plan
			engine.Prepare(&plan)
			return d, replace(NewPlan(d.c, PlanSpec{Plan: plan, Crumbs: d.spec.Crumbs}))
		}
	}
	switch k {
	case "enter", "esc":
		return d, goHome(true)
	case "l":
		return d, openLog(d.res.LogPath)
	case "q":
		return d, tea.Quit
	}
	return d, nil
}

func (d *Done) Lines() []string {
	c, t := d.c, d.c.Theme
	w := c.W
	res := d.res
	muted := t.S(t.Muted)
	reclaimed := res.Reclaimed()

	glyph, gc := "✓", t.Safe
	title := "Done"
	sub := fmt.Sprintf("%s · %s", humanize.Plural(res.Done(), "step"), humanize.Duration(res.Elapsed))
	var notes []kit.Span
	note := func(s string) { notes = append(notes, kit.Sp(muted, s)) }
	switch {
	case res.Failed >= 0:
		glyph, gc = "✗", t.Danger
		title = fmt.Sprintf("Step %d failed: %s", res.Failed+1, res.Plan.Steps[res.Failed].Title)
		note(res.Err.Error())
		// What the command itself said, so the reason is on the screen
		// and not only in the log.
		said := failureOutput(res.Steps[res.Failed].Tail, min(max(20, w-8), 80))
		for _, line := range said {
			notes = append(notes, kit.Sp(t.S(t.Text), line))
		}
		if len(said) > 0 {
			note("The full output is in the log — press l.")
		}
		if res.Failed > 0 {
			note(fmt.Sprintf("The %s before it ran and stay done.", humanize.Plural(res.Failed, "step")))
		}
	case res.Stopped:
		glyph, gc = "■", t.Muted
		title = fmt.Sprintf("Stopped after step %d of %d", res.Done(), len(res.Plan.Steps))
	case float64(reclaimed) > noise:
		title = "Reclaimed " + humanize.Bytes(reclaimed)
	case res.Trashed > 0:
		title = "Moved " + humanize.Bytes(res.Trashed) + " to the Trash"
	}
	failures := res.Failures()
	removed := false
	for _, s := range res.Steps {
		removed = removed || s.Bytes > 0
	}
	if len(failures) > 0 && !removed && res.Failed < 0 && !res.Stopped {
		glyph, gc = "✗", t.Danger
		title = "Nothing was removed"
	}
	if res.Trashed > 0 {
		note("Space comes back when the Trash is emptied — press e.")
		if float64(reclaimed) > noise {
			sub += " · " + humanize.Bytes(res.Trashed) + " in the Trash"
		}
	}

	block := []string{
		kit.Center(t, w, kit.Sp(t.S(gc), glyph)),
		kit.Center(t, w, kit.Sp(t.S(t.Strong).Bold(true), title)),
		kit.Center(t, w, kit.Sp(muted, sub)),
	}
	for _, n := range notes {
		block = append(block, kit.Center(t, w, n))
	}
	// What had to be left behind, and why.
	if n := len(failures); n > 0 {
		const show = 5
		noun := "items"
		if n == 1 {
			noun = "item"
		}
		block = append(block, kit.Blank(t, w),
			kit.Center(t, w, kit.Sp(t.S(t.Danger), fmt.Sprintf("%d %s could not be removed", n, noun))))
		room := max(20, w-8)
		shown := failures[:min(n, show)]
		for i := 0; i < len(shown); {
			// Paths left behind for the same reason share one explanation.
			j := i
			for j < len(shown) && shown[j].Reason == shown[i].Reason {
				j++
			}
			reason := shown[i].Reason
			path := func(f engine.Failure) string {
				p := c.Tilde(f.Path)
				if kit.Width(p) > room {
					p = strings.TrimRight(kit.FitLeft(p, room), " ")
				}
				return p
			}
			if one := path(shown[i]) + " — " + reason; j-i == 1 && kit.Width(one) <= room {
				block = append(block, kit.Center(t, w,
					kit.Sp(t.S(t.Text), path(shown[i])), kit.Sp(muted, " — "+reason)))
			} else {
				for _, f := range shown[i:j] {
					block = append(block, kit.Center(t, w, kit.Sp(t.S(t.Text), path(f))))
				}
				for _, line := range wrap(reason, min(room, 80)) {
					block = append(block, kit.Center(t, w, kit.Sp(muted, line)))
				}
			}
			i = j
		}
		if n > show {
			block = append(block, kit.Center(t, w,
				kit.Sp(muted, fmt.Sprintf("+%d more — press l for the log", n-show))))
		}
	}
	block = append(block, kit.Blank(t, w))

	// Free space before and after, on one scale.
	cols := min(60, max(10, w-40))
	total := d.vol.Total
	if total > 0 {
		usedB := int(math.Round(float64(total-res.FreeBefore) / float64(total) * float64(cols)))
		usedA := int(math.Round(float64(total-res.FreeAfter) / float64(total) * float64(cols)))
		usedB = min(max(usedB, 0), cols)
		usedA = min(max(usedA, 0), usedB)
		block = append(block,
			kit.Center(t, w,
				kit.CellR(muted, "before", 8), kit.Sp(t.Base(), "  "),
				kit.Sp(t.S(t.Bar), strings.Repeat("█", usedB)+strings.Repeat("░", cols-usedB)),
				kit.Sp(t.Base(), "  "), kit.Cell(muted, humanize.Bytes(res.FreeBefore)+" free", 16)),
			kit.Center(t, w,
				kit.CellR(t.S(t.Strong), "after", 8), kit.Sp(t.Base(), "  "),
				kit.Sp(t.S(t.Text), strings.Repeat("█", usedA)),
				kit.Sp(t.S(t.Strong), strings.Repeat("▒", usedB-usedA)+strings.Repeat("░", cols-usedB)),
				kit.Sp(t.Base(), "  "), kit.Cell(t.S(t.Strong), humanize.Bytes(res.FreeAfter)+" free", 16)),
			kit.Blank(t, w))
	}

	// What changed, in the recipe's own terms.
	tableW := min(64, w-8)
	row := func(label string, value []kit.Span) string {
		return kit.Center(t, w, kit.Sp(t.Base(), kit.Row(tableW, t.Base(), []kit.Span{kit.Sp(muted, label)}, value)))
	}
	if m := res.Metric; m != nil {
		block = append(block, row(m.Label, []kit.Span{
			kit.Sp(muted, humanize.Bytes(m.Before)+" → "), kit.Sp(t.S(t.Strong), humanize.Bytes(m.After)),
		}))
	}
	for _, s := range res.Plan.Summary {
		label, value, _ := strings.Cut(s, "\t")
		block = append(block, row(label, []kit.Span{kit.Sp(t.S(t.Strong), value)}))
	}

	head := []string{header(c, d.spec.crumbs(c, "Done"), nil)}
	pad := max(1, (c.H-2-len(block))/2)
	for i := 0; i < pad; i++ {
		head = append(head, kit.Blank(t, w))
	}
	head = append(head, block...)

	var hints []kit.Hint
	for _, a := range d.actions {
		hints = append(hints, kit.Hint{Key: a.Key, Label: a.Label})
	}
	hints = append(hints, kit.Hint{Key: "l", Label: "log"}, kit.Hint{Key: "q", Label: "quit"})
	return kit.Frame(t, w, c.H, head, []string{kit.Footer(t, w, hints, nil, "⏎", "back to overview")})
}

// failureOutput picks what to show of a failed command's output: its last
// lines, wrapped to the width, and no more than a few rows of them.
func failureOutput(tail []string, width int) []string {
	const lines, rows = 3, 6
	if len(tail) > lines {
		tail = tail[len(tail)-lines:]
	}
	var out []string
	for _, l := range tail {
		out = append(out, wrap(l, width)...)
	}
	if len(out) > rows {
		out = append(out[:rows-1], "…")
	}
	return out
}
