//go:build darwin

package engine

import "github.com/romus/space/pkg/recipe"

// Group is the steps of a plan that share a group name, in plan order.
type Group struct {
	Name  string
	Steps []int // indices into Plan.Steps
}

// Stage is one thing the engine does before moving on: either a single
// step on its own, or a set of groups that run side by side.
type Stage struct {
	Step   int     // the step, when Groups is empty
	Groups []Group // the groups of this stage, in order of first appearance
}

// Layout splits a plan into stages. A step without a group is a stage of
// its own. Grouped steps that follow one another form one stage, and inside
// it the steps that share a name are one group.
func Layout(p recipe.Plan) []Stage {
	var out []Stage
	for i := 0; i < len(p.Steps); {
		if p.Steps[i].Group == "" {
			out = append(out, Stage{Step: i})
			i++
			continue
		}
		st := Stage{Step: i}
		at := map[string]int{}
		for ; i < len(p.Steps) && p.Steps[i].Group != ""; i++ {
			name := p.Steps[i].Group
			g, ok := at[name]
			if !ok {
				g = len(st.Groups)
				at[name] = g
				st.Groups = append(st.Groups, Group{Name: name})
			}
			st.Groups[g].Steps = append(st.Groups[g].Steps, i)
		}
		out = append(out, st)
	}
	return out
}

// Groups lists every group of a plan, in plan order. Result.Groups uses the
// same order.
func Groups(p recipe.Plan) []Group {
	var out []Group
	for _, st := range Layout(p) {
		out = append(out, st.Groups...)
	}
	return out
}
