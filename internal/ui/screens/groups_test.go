//go:build darwin

package screens

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/romus/space/internal/engine"
	"github.com/romus/space/internal/ui/theme"
	"github.com/romus/space/pkg/recipe"
)

// repoPlan is a plan shaped like the Git one: a step for all, then the
// same few steps in every repository. Only web has an LFS cache.
func repoPlan(home string, names ...string) recipe.Plan {
	p := recipe.Plan{
		RecipeID: "git", Title: "Git", Seconds: 180, Reclaim: 6_400_000_000,
		Parallel: 4, GroupNoun: "repo",
		Toggles: []recipe.Toggle{{Key: "compact", Label: "compact .git", On: true}},
		Notes: []string{
			"skipped\tlegacy-billing — uncommitted changes · sandbox — detached HEAD",
			"untouched\tbranches on GitHub · stashes · tags · working files",
		},
		Steps: []recipe.Step{{Title: "Create the backup folder", Safe: true,
			Why: "Bundles of deleted branches are kept there for 30 days.",
			Cmd: []string{"mkdir", "-p", home + "/.local/state/space/git"}}},
	}
	for _, name := range names {
		repo := home + "/work/" + name
		git := func(args ...string) []string { return append([]string{"git", "-C", repo}, args...) }
		p.Steps = append(p.Steps,
			recipe.Step{Group: name, Title: "Back up 2 branches", Kind: "Back up branches\tone file per repo, kept 30 days", Safe: true,
				Why: "Brings a deleted branch back with git fetch.",
				Cmd: git("bundle", "create", home+"/.local/state/space/git/"+name+".bundle", "fix/login", "exp/webgl", "--not", "main")},
			recipe.Step{Group: name, Title: "Delete 5 branches", Kind: "Delete branches\t30 in total", Reversible: true,
				Why: "Merged, gone on origin, or pushed. Undo from the bundle.",
				Cmd: git("branch", "-D", "fix/login", "feat/old", "exp/webgl", "a", "b")},
			recipe.Step{Group: name, Title: "Forget old history", Kind: "Forget old history\treflog keeps deleted commits alive",
				Why: "After this the deleted commits can only come back from the bundle.",
				Cmd: git("-c", "gc.reflogExpireUnreachable=now", "reflog", "expire", "--all")},
			recipe.Step{Group: name, Title: "Compact", Kind: "Compact\tthis is where space comes back", Safe: true,
				Why: "Repacks what is left and drops what nothing points to.",
				Cmd: git("gc", "--prune=now")})
		if name == "web" {
			p.Steps = append(p.Steps, recipe.Step{Group: name, Title: "Prune the LFS cache", Kind: "Prune the LFS cache", Safe: true,
				Why: "Large files that are on the server and not checked out.",
				Cmd: git("lfs", "prune")})
		}
	}
	return p
}

var sixRepos = []string{"ml-experiments", "indexer", "web", "api", "infra-terraform", "docs"}

func foldScreen(th *theme.Theme) (*Plan, *Ctx) {
	c := testCtx(th)
	plan := repoPlan(c.Home(), sixRepos...)
	return NewPlan(c, PlanSpec{
		Plan:   plan,
		Crumbs: []string{"Recipes", "Git · ~/work"},
		Rebuild: func(o recipe.Options) (recipe.Plan, error) {
			if o.Toggle("compact", true) {
				return plan, nil
			}
			return repoPlan(c.Home(), "web"), nil
		},
	}), c
}

func key(code rune, mod tea.KeyMod) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code, Mod: mod} }

func plain(t *testing.T, s Screen, c *Ctx) string {
	t.Helper()
	text(t, s, c) // checks the shape
	var out []string
	for _, l := range s.Lines() {
		out = append(out, strings.TrimRight(ansi.Strip(l), " "))
	}
	return strings.Join(out, "\n") + "\n"
}

func TestPlanFoldsGroups(t *testing.T) {
	p, c := foldScreen(theme.DarkTheme())
	p.fold.cur = 3 // the irreversible line
	golden(t, "plan_groups.golden", plain(t, p, c))
}

func TestPlanFoldOpensOneGroup(t *testing.T) {
	p, c := foldScreen(theme.DarkTheme())
	var s Screen = p
	s = press(s, "j") // Back up branches
	s = press(s, "j") // Delete branches
	s = press(s, "l")
	if !p.fold.detail || p.cur != 2 {
		t.Fatalf("→ opens the first repo at that step: detail=%v cur=%d", p.fold.detail, p.cur)
	}
	golden(t, "plan_groups_detail.golden", plain(t, s, c))

	// tab goes to the same step of the next repo, shift+tab back, around.
	s, _ = s.Update(key(tea.KeyTab, 0))
	wantAll(t, text(t, s, c), "indexer", "repo 2 of 6 · step 2 of 4")
	s, _ = s.Update(key(tea.KeyTab, tea.ModShift))
	s, _ = s.Update(key(tea.KeyTab, tea.ModShift))
	wantAll(t, text(t, s, c), "docs", "repo 6 of 6 · step 2 of 4")

	// esc closes the repo, and the cursor is on the line it came from.
	s, cmd := s.Update(pressMsg("esc"))
	if cmd != nil || p.fold.detail || p.fold.cur != 2 {
		t.Fatalf("esc goes back to the folded plan: detail=%v cur=%d", p.fold.detail, p.fold.cur)
	}
	if _, cmd = s.Update(pressMsg("esc")); cmd == nil {
		t.Fatal("esc on the folded plan leaves the screen")
	} else if _, ok := cmd().(PopMsg); !ok {
		t.Fatal("esc on the folded plan must pop")
	}
}

func TestPlanFoldEditing(t *testing.T) {
	p, c := foldScreen(theme.DarkTheme())
	var s Screen = press(p, "j")
	s = press(s, "e")
	if s.Typing() {
		t.Fatal("a folded line stands for many commands and cannot be edited")
	}
	wantAll(t, text(t, s, c), "Open a repo with → to edit its command")

	s = press(s, "l")
	s = press(s, "e")
	if !s.Typing() {
		t.Fatal("inside a repo a command can be edited")
	}
	for _, r := range " x" {
		s = press(s, string(r))
	}
	s = press(s, "enter")
	if cmd := p.spec.Plan.Steps[1].Cmd; cmd[len(cmd)-1] != "x" || p.spec.Plan.Steps[1].Group != "ml-experiments" {
		t.Fatalf("the edited step keeps its group: %+v", p.spec.Plan.Steps[1])
	}
	if p.spec.Rebuild != nil {
		t.Fatal("an edited plan is no longer the recipe's")
	}
}

func TestPlanFoldToggleAndConfirm(t *testing.T) {
	p, c := foldScreen(theme.DarkTheme())
	var s Screen = press(p, "l")
	s = press(s, "1")
	if p.fold.detail || len(p.fold.groups) != 1 {
		t.Fatalf("a toggle folds the new plan again: detail=%v groups=%d", p.fold.detail, len(p.fold.groups))
	}
	wantAll(t, text(t, s, c), "5 steps in web", "run in 1 repo · ≈ 6.4 GB")

	next, cmd := s.Update(pressMsg("enter"))
	if cmd != nil {
		t.Fatal("the first enter must not start anything")
	}
	wantAll(t, text(t, next, c), "this cannot be undone — press enter again")
}

func TestJoinFit(t *testing.T) {
	names := []string{"web", "api", "docs", "indexer"}
	cases := map[int]string{
		40: "web · api · docs · indexer",
		20: "web · api · +2",
		9:  "web · +3",
		3:  "+4",
	}
	for w, want := range cases {
		if got := joinFit(names, w); got != want {
			t.Errorf("joinFit(%d) = %q, want %q", w, got, want)
		}
	}
}

// groupRunScreen is a run in the middle: one repo done, one failed, two
// busy, the rest waiting.
func groupRunScreen(c *Ctx) *Run {
	plan := repoPlan(c.Home(), sixRepos...)
	r := &Run{
		c: c, spec: PlanSpec{Plan: plan, Crumbs: []string{"Recipes", "Git · ~/work"}},
		eng:     c.NewEngine(nil),
		status:  make([]int, len(plan.Steps)),
		results: make([]engine.StepResult, len(plan.Steps)),
		start:   time.Now().Add(-17 * time.Second),
		groups:  newGroupRun(plan),
	}
	feed := func(evs ...any) {
		for _, ev := range evs {
			r.Update(runEventMsg{ev})
		}
	}
	ok := engine.StepResult{Ran: true, Elapsed: time.Second}
	feed(engine.StepStarted{Index: 0}, engine.StepFinished{Index: 0, Result: ok})
	// ml-experiments: steps 1–4, all done.
	for i := 1; i <= 4; i++ {
		feed(engine.StepStarted{Index: i}, engine.StepFinished{Index: i, Result: ok})
	}
	feed(engine.GroupFinished{Index: 0, Result: engine.GroupResult{Name: "ml-experiments", Steps: []int{1, 2, 3, 4}, Done: 4, Failed: -1, Elapsed: 14 * time.Second}})
	// indexer: steps 5–8, the second fails.
	boom := errors.New("git exited with status 128")
	feed(engine.StepStarted{Index: 5}, engine.StepFinished{Index: 5, Result: ok},
		engine.StepStarted{Index: 6}, engine.StepOutput{Index: 6, Line: "error: branch 'a' not found"},
		engine.StepFinished{Index: 6, Result: engine.StepResult{Ran: true, Err: boom}},
		engine.GroupFinished{Index: 1, Result: engine.GroupResult{Name: "indexer", Steps: []int{5, 6, 7, 8}, Done: 1, Failed: 6, Err: boom}})
	// web (9–13) and api (14–17) are busy, and print in turn.
	feed(engine.StepStarted{Index: 9}, engine.StepFinished{Index: 9, Result: ok}, engine.StepStarted{Index: 10},
		engine.StepStarted{Index: 14},
		engine.StepOutput{Index: 10, Line: "Deleted branch fix/login (was 3f9a1c0)."},
		engine.StepOutput{Index: 14, Line: "Enumerating objects: 412, done."},
		engine.StepOutput{Index: 10, Line: "Deleted branch feat/old (was 9e04d7a)."})
	return r
}

func TestRunWithGroups(t *testing.T) {
	c := testCtx(theme.LightTheme())
	r := groupRunScreen(c)
	s := text(t, r, c)
	wantAll(t, s, "Recipes › Git · ~/work › Running", "1 of 6 repos", "· 1 failed", "running web · api",
		"✓  Create the backup folder", "✓  ml-experiments", "4/4", "14 s",
		"✗  indexer", "1/4", "Delete 5 branches: git exited with status 128", "failed",
		"web", "1/5", "·  docs", "0/4",
		// The box follows web: the row that began first stays in view.
		"git -C ~/work/web branch -D fix/login feat/old exp/webgl a b",
		"Deleted branch feat/old (was 9e04d7a).",
		"↑↓ pick a repo", "ctrl+c stop after the running steps")
	if strings.Contains(s, "Enumerating objects") {
		t.Fatal("the box shows one row's output, not everyone's")
	}

	// The cursor stays on web while it runs, whoever prints.
	r.Update(runEventMsg{engine.StepOutput{Index: 14, Line: "Counting objects: 100%"}})
	if r.groups.rows[r.groups.cur].name != "web" {
		t.Fatal("following must not jump between busy rows")
	}
	// When web ends, it moves to a row that is still running.
	r.Update(runEventMsg{engine.GroupFinished{Index: 2, Result: engine.GroupResult{Name: "web", Steps: []int{9, 10, 11, 12, 13}, Done: 2, Failed: -1}}})
	if r.groups.rows[r.groups.cur].name != "api" {
		t.Fatalf("following moves on to a running row, got %s", r.groups.rows[r.groups.cur].name)
	}
	wantAll(t, text(t, r, c), "■  web", "not finished", "Counting objects: 100%")

	// The user picks a row: following stops until esc.
	r.Update(pressMsg("k"))
	r.Update(pressMsg("k"))
	wantAll(t, text(t, r, c), "error: branch 'a' not found", "esc follow")
	r.Update(runEventMsg{engine.StepOutput{Index: 14, Line: "more"}})
	if r.groups.rows[r.groups.cur].name != "indexer" {
		t.Fatal("a picked row stays picked")
	}
	r.Update(pressMsg("esc"))
	if r.groups.rows[r.groups.cur].name != "api" {
		t.Fatal("esc goes back to following")
	}

	r.cancel = func() {}
	r.Interrupt()
	wantAll(t, text(t, r, c), "ctrl+c stopping… again to cut short")
}

func TestDoneWithGroups(t *testing.T) {
	c := testCtx(theme.DarkTheme())
	c.Root = t.TempDir()
	plan := repoPlan(c.Home(), sixRepos...)
	spec := PlanSpec{Plan: plan, Crumbs: []string{"Recipes", "Git · ~/work"}}
	steps := make([]engine.StepResult, len(plan.Steps))
	for i := range steps {
		steps[i] = engine.StepResult{Ran: true}
	}
	var groups []engine.GroupResult
	for _, g := range engine.Groups(plan) {
		groups = append(groups, engine.GroupResult{Name: g.Name, Steps: g.Steps, Done: len(g.Steps), Failed: -1})
	}
	ok := engine.Result{Plan: plan, Steps: steps, Groups: groups, Failed: -1, Elapsed: 182 * time.Second,
		FreeBefore: 82_000_000_000, FreeAfter: 88_400_000_000}
	wantAll(t, text(t, NewDone(c, spec, ok), c), "✓", "Reclaimed 6.4 GB", "6 repos · 26 steps · 3 m 2 s")

	boom := errors.New("git exited with status 128")
	some := ok
	some.Steps = append([]engine.StepResult{}, steps...)
	some.Steps[6] = engine.StepResult{Ran: true, Err: boom, Tail: []string{"error: branch 'a' not found"}}
	some.Steps[7], some.Steps[8] = engine.StepResult{}, engine.StepResult{}
	some.Groups = append([]engine.GroupResult{}, groups...)
	some.Groups[1] = engine.GroupResult{Name: "indexer", Steps: []int{5, 6, 7, 8}, Done: 1, Failed: 6, Err: boom}
	s := text(t, NewDone(c, spec, some), c)
	wantAll(t, s, "!", "Reclaimed 6.4 GB", "5 of 6 repos · 23 steps",
		"1 repo was not finished", "indexer — step 2 of 4, Delete 5 branches: git exited with status 128",
		"error: branch 'a' not found")

	none := some
	none.FreeAfter = none.FreeBefore
	none.Groups = nil
	for _, g := range groups {
		none.Groups = append(none.Groups, engine.GroupResult{Name: g.Name, Steps: g.Steps, Failed: g.Steps[0], Err: boom})
	}
	s = text(t, NewDone(c, spec, none), c)
	wantAll(t, s, "✗", "No repo was finished", "6 repos were not finished", "+3 more — press l for the log")

	stopped := ok
	stopped.Stopped = true
	stopped.Groups = append([]engine.GroupResult{}, groups...)
	stopped.Groups[4] = engine.GroupResult{Name: "infra-terraform", Steps: groups[4].Steps, Done: 1, Failed: -1}
	stopped.Groups[5] = engine.GroupResult{Name: "docs", Steps: groups[5].Steps, Failed: -1}
	wantAll(t, text(t, NewDone(c, spec, stopped), c), "■", "Stopped: 4 of 6 repos finished")
}

func TestGroupScreensSurviveAnyWindow(t *testing.T) {
	var names []string
	for i := range 30 {
		names = append(names, fmt.Sprintf("repository-number-%02d", i))
	}
	for _, size := range [][2]int{{80, 20}, {96, 24}, {160, 50}, {40, 10}} {
		c := testCtx(theme.DarkTheme())
		c.W, c.H = size[0], size[1]
		plan := repoPlan(c.Home(), names...)
		p := NewPlan(c, PlanSpec{Plan: plan, Crumbs: []string{"Recipes", "Git"}})
		text(t, p, c)
		text(t, press(p, "l"), c)

		r := &Run{c: c, spec: PlanSpec{Plan: plan}, eng: c.NewEngine(nil), start: time.Now(),
			status: make([]int, len(plan.Steps)), results: make([]engine.StepResult, len(plan.Steps)),
			groups: newGroupRun(plan)}
		r.Update(runEventMsg{engine.StepStarted{Index: 40}})
		for range 40 {
			r.Update(pressMsg("j"))
			text(t, r, c)
		}
	}
}
