//go:build darwin

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/romus/space/internal/engine"
	"github.com/romus/space/internal/humanize"
	"github.com/romus/space/pkg/recipe"
)

// run executes a plan with plain text progress.
func (a *app) run(ctx context.Context, out io.Writer, r recipe.Recipe, plan recipe.Plan) error {
	eng := &engine.Engine{Home: a.paths.Home, Volume: a.paths.Home, LogPath: a.paths.LogFile(a.env.Now())}
	if m, ok := r.(recipe.Measurer); ok {
		eng.Measure = m.Measure
	}
	fmt.Fprintln(out)
	res := eng.Run(ctx, plan, progress(out, plan))
	fmt.Fprintln(out)
	return outcome(out, res, a.paths.Home)
}

// place is where a step stands inside its group: "web 2/6".
type place struct {
	group string
	n, of int
}

func places(plan recipe.Plan) map[int]place {
	out := map[int]place{}
	for _, g := range engine.Groups(plan) {
		for n, i := range g.Steps {
			out[i] = place{g.Name, n + 1, len(g.Steps)}
		}
	}
	return out
}

// progress prints what the engine reports, line by line. Lines of a step
// inside a group carry the group's name: several groups print at once.
func progress(out io.Writer, plan recipe.Plan) func(engine.Event) {
	at := places(plan)
	return func(ev engine.Event) {
		switch ev := ev.(type) {
		case engine.StepStarted:
			if p, ok := at[ev.Index]; ok {
				fmt.Fprintf(out, "→ %s %d/%d %s\n", p.group, p.n, p.of, plan.Steps[ev.Index].Title)
				return
			}
			fmt.Fprintf(out, "→ %d/%d %s\n", ev.Index+1, len(plan.Steps), plan.Steps[ev.Index].Title)
		case engine.StepOutput:
			if p, ok := at[ev.Index]; ok {
				fmt.Fprintf(out, "    %s | %s\n", p.group, ev.Line)
				return
			}
			fmt.Fprintf(out, "    %s\n", ev.Line)
		case engine.StepFinished:
			if _, ok := at[ev.Index]; ok {
				return // the group reports once, when it ends
			}
			if ev.Result.Err != nil {
				fmt.Fprintf(out, "  ✗ %v\n", ev.Result.Err)
			} else {
				fmt.Fprintf(out, "  ✓ %s\n", humanize.Duration(ev.Result.Elapsed))
			}
		case engine.GroupFinished:
			g := ev.Result
			switch {
			case g.Finished():
				fmt.Fprintf(out, "  ✓ %s %s\n", g.Name, humanize.Duration(g.Elapsed))
			case g.Failed >= 0:
				fmt.Fprintf(out, "  ✗ %s: %v\n", g.Name, g.Err)
			default:
				fmt.Fprintf(out, "  ■ %s: not finished\n", g.Name)
			}
		}
	}
}

// outcome prints what a run achieved and returns an error when part of it
// did not happen.
func outcome(out io.Writer, res engine.Result, home string) error {
	if res.Metric != nil {
		fmt.Fprintf(out, "%s: %s → %s\n", res.Metric.Label, humanize.Bytes(res.Metric.Before), humanize.Bytes(res.Metric.After))
	}
	fmt.Fprintf(out, "Free space: %s → %s (reclaimed %s)\n", humanize.Bytes(res.FreeBefore),
		humanize.Bytes(res.FreeAfter), humanize.Bytes(res.Reclaimed()))
	if res.Trashed > 0 {
		fmt.Fprintf(out, "%s moved to the Trash: space comes back when you empty it.\n", humanize.Bytes(res.Trashed))
	}
	failures := res.Failures()
	if n := len(failures); n > 0 {
		noun := "items"
		if n == 1 {
			noun = "item"
		}
		fmt.Fprintf(out, "%d %s could not be removed:\n", n, noun)
		for _, f := range failures {
			fmt.Fprintf(out, "  %s — %s\n", humanize.Tilde(f.Path, home), f.Reason)
		}
	}
	fmt.Fprintf(out, "Log: %s\n", humanize.Tilde(res.LogPath, home))
	if res.Failed >= 0 {
		return fmt.Errorf("step %d failed: %w", res.Failed+1, res.Err)
	}
	if failed := res.FailedGroups(); len(failed) > 0 {
		names := make([]string, len(failed))
		for i, g := range failed {
			names[i] = g.Name
		}
		return fmt.Errorf("%d of %s were not finished: %s", len(failed),
			humanize.Plural(len(res.Groups), res.Plan.Noun()), strings.Join(names, ", "))
	}
	if len(failures) > 0 {
		return errors.New("some items could not be removed")
	}
	return nil
}
