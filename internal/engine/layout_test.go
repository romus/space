//go:build darwin

package engine

import (
	"fmt"
	"strings"
	"testing"

	"github.com/romus/space/pkg/recipe"
)

// groupPlan builds a plan from "group:command" pairs; ":command" is a step
// on its own.
func groupPlan(parallel int, spec ...string) recipe.Plan {
	p := recipe.Plan{Title: "Test", Parallel: parallel}
	for _, s := range spec {
		group, name, _ := strings.Cut(s, ":")
		p.Steps = append(p.Steps, recipe.Step{Title: "run " + name, Cmd: []string{name, "arg"}, Group: group})
	}
	return p
}

func layoutText(p recipe.Plan) string {
	var parts []string
	for _, st := range Layout(p) {
		if len(st.Groups) == 0 {
			parts = append(parts, fmt.Sprint(st.Step))
			continue
		}
		var gs []string
		for _, g := range st.Groups {
			gs = append(gs, fmt.Sprintf("%s%v", g.Name, g.Steps))
		}
		parts = append(parts, "{"+strings.Join(gs, " ")+"}")
	}
	return strings.Join(parts, " ")
}

func TestLayout(t *testing.T) {
	cases := []struct {
		name string
		spec []string
		want string
	}{
		{"no groups", []string{":a", ":b"}, "0 1"},
		{"empty", nil, ""},
		{"one stage", []string{"x:a", "x:b", "y:c"}, "{x[0 1] y[2]}"},
		{"steps around a stage", []string{":pre", "x:a", "y:b", ":post"}, "0 {x[1] y[2]} 3"},
		{"interleaved names keep their order", []string{"x:a", "y:b", "x:c", "y:d"}, "{x[0 2] y[1 3]}"},
		{"a lone step splits two stages", []string{"x:a", ":mid", "x:b"}, "{x[0]} 1 {x[2]}"},
	}
	for _, c := range cases {
		if got := layoutText(groupPlan(4, c.spec...)); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	if n := len(Groups(groupPlan(4, "x:a", ":mid", "x:b", "y:c"))); n != 3 {
		t.Fatalf("groups across stages: got %d, want 3", n)
	}
}
