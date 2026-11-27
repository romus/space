//go:build darwin

package screens

import (
	"fmt"
	"strings"

	"github.com/romus/space/internal/engine"
	"github.com/romus/space/internal/humanize"
	"github.com/romus/space/pkg/recipe"
)

// foldLine is one line of a folded plan: a step on its own, or the same
// kind of step in every group.
type foldLine struct {
	title string // "Delete branches"
	note  string // "118 in total"
	steps []int  // every step folded into this line, in plan order
	worst int    // the step with the most severe tag
}

// example is the step whose command stands for the whole line.
func (l foldLine) example() int { return l.steps[0] }

// planFold is the folded view of a plan with groups: the same few steps in
// each of many groups are shown once.
type planFold struct {
	lines    []foldLine
	groups   []engine.Group
	of       map[int]int // step → its group
	cur, off int
	detail   bool // one group's steps are open, drawn step by step
}

// newFold folds a plan. It is nil for a plan without groups.
func newFold(p recipe.Plan) *planFold {
	groups := engine.Groups(p)
	if len(groups) == 0 {
		return nil
	}
	f := &planFold{groups: groups, of: map[int]int{}}
	for gi, g := range groups {
		for _, i := range g.Steps {
			f.of[i] = gi
		}
	}
	for _, st := range engine.Layout(p) {
		if len(st.Groups) == 0 {
			f.lines = append(f.lines, foldLine{title: p.Steps[st.Step].Title, steps: []int{st.Step}, worst: st.Step})
			continue
		}
		f.lines = append(f.lines, foldStage(p, st)...)
	}
	return f
}

// foldStage folds the steps of one stage by kind. The lines keep the order
// the steps have inside a group; a kind that only some groups have takes
// its place after the step it follows there, so a group with fewer steps
// coming first does not shuffle the plan.
func foldStage(p recipe.Plan, st engine.Stage) []foldLine {
	var lines []foldLine
	find := func(title string) int {
		for n, l := range lines {
			if l.title == title {
				return n
			}
		}
		return -1
	}
	for _, g := range st.Groups {
		after := -1 // the line of this group's previous step
		for _, i := range g.Steps {
			s := p.Steps[i]
			title, note := s.Title, ""
			if s.Kind != "" {
				title, note, _ = strings.Cut(s.Kind, "\t")
			}
			n := find(title)
			if n < 0 {
				n = after + 1
				lines = append(lines[:n], append([]foldLine{{title: title, note: note, worst: i}}, lines[n:]...)...)
			}
			l := &lines[n]
			l.steps = append(l.steps, i)
			if severity(s) > severity(p.Steps[l.worst]) {
				l.worst = i
			}
			after = n
		}
	}
	return lines
}

// severity orders the tags of steps: the higher, the more it matters.
func severity(s recipe.Step) int {
	switch s.Tag() {
	case "IRREVERSIBLE":
		return 3
	case "REVERSIBLE":
		return 2
	case "SAFE":
		return 1
	}
	return 0
}

// line returns the folded line a step belongs to.
func (f *planFold) line(step int) int {
	for n, l := range f.lines {
		for _, i := range l.steps {
			if i == step {
				return n
			}
		}
	}
	return 0
}

// jump moves from a step to the same kind of step in the next group, or
// the one before it.
func (f *planFold) jump(step, dir int) int {
	steps := f.lines[f.line(step)].steps
	for n, i := range steps {
		if i == step {
			return steps[(n+dir+len(steps))%len(steps)]
		}
	}
	return step
}

// headline sums the plan up: "Same 6 steps in each of 21 repos".
func (f *planFold) headline(p recipe.Plan) (title, sub string) {
	most, same := 0, true
	for _, g := range f.groups {
		if most != 0 && len(g.Steps) != most {
			same = false
		}
		most = max(most, len(g.Steps))
	}
	steps := humanize.Plural(most, "step")
	switch {
	case len(f.groups) == 1:
		title = steps + " in " + f.groups[0].Name
	case same:
		title = fmt.Sprintf("Same %s in each of %s", steps, humanize.Plural(len(f.groups), p.Noun()))
	default:
		title = fmt.Sprintf("Up to %s in each of %s", steps, humanize.Plural(len(f.groups), p.Noun()))
	}
	if p.Parallel > 1 && len(f.groups) > 1 {
		sub = fmt.Sprintf("%d at a time · ", p.Parallel)
	}
	sub += "≈ " + humanize.Duration(secs(p.Seconds)) + " · nothing has run yet"
	return title, sub
}

// where says which group the step under the cursor belongs to.
func (f *planFold) where(p recipe.Plan, step int) (title, sub string) {
	gi, ok := f.of[step]
	if !ok {
		return "Once, for all of them", fmt.Sprintf("step %d of %d", step+1, len(p.Steps))
	}
	g := f.groups[gi]
	n := 0
	for k, i := range g.Steps {
		if i == step {
			n = k
		}
	}
	return g.Name, fmt.Sprintf("%s %d of %d · step %d of %d", p.Noun(), gi+1, len(f.groups), n+1, len(g.Steps))
}

// joinFit joins names with dots, and when they do not fit in w columns
// says how many were left out: "web · api · +19".
func joinFit(names []string, w int) string {
	const sep = " · "
	all := strings.Join(names, sep)
	if len([]rune(all)) <= w {
		return all
	}
	out := ""
	for i, name := range names {
		rest := fmt.Sprintf("+%d", len(names)-i)
		next := out + name + sep
		if len([]rune(next+rest)) > w {
			return out + rest
		}
		out = next
	}
	return out
}
