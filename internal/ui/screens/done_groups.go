//go:build darwin

package screens

import (
	"fmt"
	"strings"

	"github.com/romus/space/internal/engine"
	"github.com/romus/space/internal/humanize"
	"github.com/romus/space/internal/ui/kit"
)

// groupSub is the line under the title for a run with groups:
// "19 of 21 repos · 114 steps · 3 m 2 s".
func groupSub(res engine.Result) string {
	what := humanize.Plural(len(res.Groups), res.Plan.Noun())
	if n := res.FinishedGroups(); n < len(res.Groups) {
		what = fmt.Sprintf("%d of %s", n, what)
	}
	return fmt.Sprintf("%s · %s · %s", what, humanize.Plural(res.Done(), "step"), humanize.Duration(res.Elapsed))
}

// groupStopped is the title of a run with groups that the user stopped.
func groupStopped(res engine.Result) string {
	return fmt.Sprintf("Stopped: %d of %s finished", res.FinishedGroups(),
		humanize.Plural(len(res.Groups), res.Plan.Noun()))
}

// groupFailures lists the groups a failing step ended: which step it was,
// and what the command itself said.
func (d *Done) groupFailures() []string {
	c, t := d.c, d.c.Theme
	w := c.W
	res := d.res
	failed := res.FailedGroups()
	if len(failed) == 0 {
		return nil
	}
	muted := t.S(t.Muted)
	noun := res.Plan.Noun()
	head := fmt.Sprintf("%s were not finished", humanize.Plural(len(failed), noun))
	if len(failed) == 1 {
		head = "1 " + noun + " was not finished"
	}
	out := []string{kit.Blank(t, w), kit.Center(t, w, kit.Sp(t.S(t.Danger), head))}

	const show = 3
	room := max(20, min(w-8, 100))
	for _, g := range failed[:min(len(failed), show)] {
		at := 0
		for n, i := range g.Steps {
			if i == g.Failed {
				at = n
			}
		}
		why := fmt.Sprintf(" — step %d of %d, %s: %v", at+1, len(g.Steps), res.Plan.Steps[g.Failed].Title, g.Err)
		out = append(out, kit.Center(t, w,
			kit.Sp(t.S(t.Text), g.Name), kit.Sp(muted, strings.TrimRight(kit.Fit(why, max(0, room-kit.Width(g.Name))), " "))))
		if tail := res.Steps[g.Failed].Tail; len(tail) > 0 {
			said := strings.TrimSpace(strings.ReplaceAll(tail[len(tail)-1], "\t", " "))
			out = append(out, kit.Center(t, w, kit.Sp(muted, strings.TrimRight(kit.Fit(said, room), " "))))
		}
	}
	if n := len(failed) - show; n > 0 {
		out = append(out, kit.Center(t, w, kit.Sp(muted, fmt.Sprintf("+%d more — press l for the log", n))))
	}
	return out
}
