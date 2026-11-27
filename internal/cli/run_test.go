//go:build darwin

package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/romus/space/internal/engine"
	"github.com/romus/space/pkg/recipe"
)

func groupedPlan() recipe.Plan {
	return recipe.Plan{Title: "Git", Parallel: 4, GroupNoun: "repo", Seconds: 60,
		Notes: []string{"skipped\tlegacy — uncommitted changes"},
		Steps: []recipe.Step{
			{Title: "Create the backup folder", Cmd: []string{"mkdir", "-p", "/b"}, Safe: true},
			{Title: "Delete 2 branches", Group: "web", Cmd: []string{"git", "branch", "-D", "a", "b"}, Reversible: true},
			{Title: "Compact", Group: "web", Cmd: []string{"git", "gc"}, Safe: true},
			{Title: "Compact", Group: "api", Cmd: []string{"git", "gc"}, Safe: true},
		}}
}

func TestPrintPlanWithGroups(t *testing.T) {
	var b bytes.Buffer
	printPlan(&b, groupedPlan(), "")
	got := b.String()
	for _, want := range []string{
		"Git — 4 steps in 2 repos, 4 at a time · ≈ 1 m 0 s",
		"\n── web ──\n",
		"\n── api ──\n",
		" 2  Delete 2 branches",
		"skipped: legacy — uncommitted changes",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("plan misses %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "── web ──") > strings.Index(got, " 2  Delete") {
		t.Errorf("a group's name comes before its steps:\n%s", got)
	}
	if q := runQuestion(groupedPlan()); q != "Run 4 steps in 2 repos?" {
		t.Errorf("question: %q", q)
	}
	if q := runQuestion(recipe.Plan{Steps: make([]recipe.Step, 1)}); q != "Run 1 step?" {
		t.Errorf("question without groups: %q", q)
	}
}

func TestProgressNamesTheGroup(t *testing.T) {
	plan := groupedPlan()
	var b bytes.Buffer
	say := progress(&b, plan)
	say(engine.StepStarted{Index: 0})
	say(engine.StepOutput{Index: 0, Line: "made"})
	say(engine.StepFinished{Index: 0, Result: engine.StepResult{Ran: true, Elapsed: 2 * time.Second}})
	say(engine.StepStarted{Index: 2})
	say(engine.StepOutput{Index: 2, Line: "packing"})
	say(engine.StepFinished{Index: 2, Result: engine.StepResult{Ran: true}})
	say(engine.GroupFinished{Result: engine.GroupResult{Name: "web", Steps: []int{1, 2}, Done: 2, Failed: -1, Elapsed: 3 * time.Second}})
	say(engine.GroupFinished{Result: engine.GroupResult{Name: "api", Steps: []int{3}, Failed: 3, Err: errors.New("git exited with status 128")}})
	want := `→ 1/4 Create the backup folder
    made
  ✓ 2 s
→ web 2/2 Compact
    web | packing
  ✓ web 3 s
  ✗ api: git exited with status 128
`
	if got := b.String(); got != want {
		t.Fatalf("progress:\n%s\nwant:\n%s", got, want)
	}
}

func TestOutcomeReportsUnfinishedGroups(t *testing.T) {
	plan := groupedPlan()
	res := engine.Result{Plan: plan, Failed: -1, Steps: make([]engine.StepResult, 4), Groups: []engine.GroupResult{
		{Name: "web", Steps: []int{1, 2}, Done: 2, Failed: -1},
		{Name: "api", Steps: []int{3}, Failed: 3, Err: errors.New("boom")},
	}}
	var b bytes.Buffer
	err := outcome(&b, res, "")
	if err == nil || err.Error() != "1 of 2 repos were not finished: api" {
		t.Fatalf("error: %v", err)
	}
	res.Groups[1] = engine.GroupResult{Name: "api", Steps: []int{3}, Done: 1, Failed: -1}
	if err := outcome(&b, res, ""); err != nil {
		t.Fatalf("every group finished, yet: %v", err)
	}
}
