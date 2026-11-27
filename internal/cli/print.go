//go:build darwin

package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/romus/space/internal/engine"
	"github.com/romus/space/internal/humanize"
	"github.com/romus/space/pkg/recipe"
)

// size says how big a plan is: "5 steps", or "126 steps in 21 repos".
func size(p recipe.Plan) string {
	what := humanize.Plural(len(p.Steps), "step")
	if n := len(engine.Groups(p)); n > 0 {
		what += " in " + humanize.Plural(n, p.Noun())
	}
	return what
}

// runQuestion is what the user is asked before a plan runs.
func runQuestion(p recipe.Plan) string { return "Run " + size(p) + "?" }

// printPlan describes a plan in plain text. The steps of a group come under
// the group's name.
func printPlan(out io.Writer, p recipe.Plan, home string) {
	what := size(p)
	if p.Parallel > 1 && len(engine.Groups(p)) > 0 {
		what += fmt.Sprintf(", %d at a time", p.Parallel)
	}
	fmt.Fprintf(out, "%s — %s · ≈ %s · nothing has run yet\n", p.Title, what,
		humanize.Duration(time.Duration(p.Seconds)*time.Second))
	for _, st := range engine.Layout(p) {
		if len(st.Groups) == 0 {
			printStep(out, p, st.Step, home)
			continue
		}
		for _, g := range st.Groups {
			fmt.Fprintf(out, "\n── %s ──\n", g.Name)
			for _, i := range g.Steps {
				printStep(out, p, i, home)
			}
		}
	}
	for _, n := range p.Notes {
		label, value, _ := strings.Cut(n, "\t")
		fmt.Fprintf(out, "\n%s: %s\n", label, value)
	}
	switch {
	case p.Metric != nil:
		fmt.Fprintf(out, "\n%s: %s → ≈ %s\n", p.Metric.Label, humanize.Bytes(p.Metric.Before), humanize.Bytes(p.Metric.After))
	case p.Reclaim > 0 || p.ToTrash > 0:
		fmt.Fprintf(out, "\nFrees ≈ %s, moves %s to the Trash.\n", humanize.Bytes(p.Reclaim), humanize.Bytes(p.ToTrash))
	}
}

func printStep(out io.Writer, p recipe.Plan, i int, home string) {
	s := p.Steps[i]
	fmt.Fprintf(out, "\n%2d  %-58s %s\n", i+1, s.Title, s.Tag())
	fmt.Fprintf(out, "    $ %s\n", engine.Display(s, home))
	if s.Why != "" {
		fmt.Fprintf(out, "    %s\n", s.Why)
	}
}
