//go:build darwin

package screens

import (
	"fmt"
	"strings"
	"time"

	"github.com/romus/space/internal/engine"
	"github.com/romus/space/internal/humanize"
	"github.com/romus/space/internal/ui/kit"
)

// groupLines draws a run with groups: one row per group, with the step it
// is at, and the output of the row under the cursor.
func (r *Run) groupLines() []string {
	c, t := r.c, r.c.Theme
	w := c.W
	g := r.groups
	noun := r.spec.Plan.Noun()

	top := []string{
		header(c, r.spec.crumbs(c, "Running"), []kit.Span{kit.Sp(t.S(t.Muted), humanize.Clock(time.Since(r.start)))}),
		kit.Blank(t, w),
		r.groupProgress(),
		kit.Blank(t, w),
	}
	box := r.groupBox()
	visible := max(1, c.H-len(top)-len(box)-1-4)
	g.off = kit.Window(g.cur, g.off, len(g.rows), visible)
	for i := g.off; i < len(g.rows) && i < g.off+visible; i++ {
		top = append(top, r.groupRow(i))
	}
	top = append(top, kit.Blank(t, w))
	top = append(top, box...)

	stop := "stop after the running steps"
	if r.stopping {
		stop = "stopping… again to cut short"
	}
	hints := []kit.Hint{{"↑↓", "pick a " + noun}, {"l", "full log"}, {"ctrl+c", stop}}
	if !g.follow {
		hints[0] = kit.Hint{Key: "esc", Label: "follow"}
	}
	return kit.Frame(t, w, c.H, top, r.bottom(hints))
}

// groupProgress is the bar, how many groups are done, and which are busy.
func (r *Run) groupProgress() string {
	c, t := r.c, r.c.Theme
	g := r.groups
	const cols = 26
	done := 0
	for _, s := range r.status {
		if s == stepDone || s == stepFailed {
			done++
		}
	}
	filled := 0
	if n := len(r.status); n > 0 {
		filled = done * cols / n
	}
	bar := strings.Repeat("█", filled)
	if filled < cols {
		bar += "▌" + strings.Repeat("░", cols-filled-1)
	}
	finished, total := g.count(stepDone)
	failed, _ := g.count(stepFailed)
	spans := []kit.Span{
		kit.Sp(t.S(t.Strong), bar),
		kit.Sp(t.S(t.Strong), fmt.Sprintf("  %d of %s", finished, humanize.Plural(total, r.spec.Plan.Noun()))),
	}
	if failed > 0 {
		spans = append(spans, kit.Sp(t.S(t.Danger), fmt.Sprintf(" · %d failed", failed)))
	}
	var busy []string
	for _, row := range g.rows {
		if row.group >= 0 && row.state == stepRun {
			busy = append(busy, row.name)
		}
	}
	if len(busy) > 0 {
		spans = append(spans, kit.Sp(t.S(t.Muted), " · running "+strings.Join(busy, " · ")))
	}
	return kit.Row(c.W, t.Base(), spans, nil)
}

// groupRow draws one row: its mark, its name, how far it is, the step it
// is at, and how it ended.
func (r *Run) groupRow(i int) string {
	c, t := r.c, r.c.Theme
	w := c.W
	steps := r.spec.Plan.Steps
	row := r.groups.rows[i]

	st := t.S
	if i == r.groups.cur {
		st = t.Sel
	}
	glyph, gc, nc := "·", t.Faint, t.Muted
	res := ""
	switch row.state {
	case stepDone:
		glyph, gc, nc = "✓", t.Safe, t.Strong
		res = humanize.Duration(row.elapsed)
		if row.bytes > 0 {
			res = humanize.Bytes(row.bytes) + " · " + res
		}
		if row.failures > 0 {
			glyph, gc, res = "!", t.Danger, fmt.Sprintf("%d failed", row.failures)
		}
	case stepFailed:
		glyph, gc, nc, res = "✗", t.Danger, t.Strong, "failed"
	case stepStopped:
		glyph, gc, res = "■", t.Muted, "not finished"
	case stepRun:
		glyph, gc, nc = kit.Spinner(c.Frame), t.Strong, t.Strong
	}

	nameW := min(32, max(12, w/4))
	far, at := "", ""
	if row.group >= 0 {
		far = fmt.Sprintf("%d/%d", row.done, len(row.steps))
		if row.cur >= 0 && row.state != stepDone {
			at = steps[row.cur].Title
		}
		if row.err != nil {
			at += ": " + row.err.Error()
		}
	} else {
		at = engine.Display(steps[row.steps[0]], c.Home())
	}
	return kit.Row(w, st(t.Text), []kit.Span{
		kit.Cell(st(t.Text), "", 1), kit.Cell(st(gc), glyph, 3),
		kit.Cell(st(nc), row.name, nameW), kit.Cell(st(t.Text), "", 1),
		kit.CellR(st(t.Muted), far, 6), kit.Cell(st(t.Text), "", 2),
		kit.Sp(st(t.Faint), kit.Fit(at, max(0, w-nameW-13-15))),
	}, []kit.Span{kit.CellR(st(t.Muted), res, 13), kit.Cell(st(t.Text), "", 1)})
}

// groupBox is the command and the last lines printed by the row under the
// cursor. It always has the same height, so the list above does not jump.
func (r *Run) groupBox() []string {
	c, t := r.c, r.c.Theme
	w := c.W
	const show = 4
	row := r.groups.rows[r.groups.cur]
	head := row.name
	if row.cur >= 0 {
		head = engine.Display(r.spec.Plan.Steps[row.cur], c.Home())
	}
	lines := []string{kit.Row(w-4, t.Base(), []kit.Span{kit.Sp(t.S(t.Faint), head)}, nil)}
	tail := row.tail
	if len(tail) > show {
		tail = tail[len(tail)-show:]
	}
	for i := range show {
		if i >= len(tail) {
			lines = append(lines, kit.Blank(t, w-4))
			continue
		}
		st := t.S(t.Muted)
		if i == len(tail)-1 {
			st = t.S(t.Subtle)
		}
		lines = append(lines, kit.Row(w-4, t.Base(), []kit.Span{kit.Sp(st, strings.ReplaceAll(tail[i], "\t", "  "))}, nil))
	}
	return kit.Box(t, w, lines)
}
