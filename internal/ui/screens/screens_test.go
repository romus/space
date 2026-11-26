//go:build darwin

package screens

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/romus/space/internal/engine"
	"github.com/romus/space/internal/recipes/podman"
	"github.com/romus/space/internal/ui/theme"
	"github.com/romus/space/pkg/recipe"
)

// recorded answers podman commands from the recipe's recorded output.
type recorded struct{}

func (recorded) LookPath(name string) (string, error) { return "/opt/homebrew/bin/" + name, nil }

func (recorded) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	line := name + " " + strings.Join(args, " ")
	files := map[string]string{
		"podman machine list --format json":        "machine-list.json",
		"podman images --format json":              "images.json",
		"podman ps --all --size --format json":     "ps.json",
		"podman system df --verbose --format json": "df-verbose.json",
		"podman system df --format json":           "df.json",
	}
	if line == "podman --version" {
		return []byte("podman version 5.3.1\n"), nil
	}
	if f, ok := files[line]; ok {
		return os.ReadFile(filepath.Join("../../recipes/podman/testdata", f))
	}
	return nil, errors.New("unexpected: " + line)
}

// text renders a screen without colour and checks its shape on the way.
func text(t *testing.T, s Screen, c *Ctx) string {
	t.Helper()
	lines := s.Lines()
	if len(lines) != c.H {
		t.Fatalf("%d lines, want %d", len(lines), c.H)
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w != c.W {
			t.Fatalf("line %d is %d columns, want %d: %q", i+1, w, c.W, ansi.Strip(l))
		}
	}
	return ansi.Strip(strings.Join(lines, "\n"))
}

func wantAll(t *testing.T, screen string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(screen, p) {
			t.Fatalf("screen does not show %q:\n%s", p, screen)
		}
	}
}

func podmanScreen(t *testing.T) (*Podman, *Ctx) {
	t.Helper()
	home := t.TempDir()
	disk := filepath.Join(home, ".local/share/containers/podman/machine/applehv/podman-machine-default-arm64.raw")
	if err := os.MkdirAll(filepath.Dir(disk), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(disk, make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	c := testCtx(theme.DarkTheme())
	c.Paths.Home = home
	c.Env = recipe.Env{Home: home, Run: recorded{}, Now: func() time.Time { return time.Unix(1795500000, 0) }}

	r := &podman.Recipe{}
	if !r.Detect(context.Background(), c.Env) {
		t.Fatal("not detected")
	}
	items, err := r.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return NewPodman(c, &RecipeState{R: r, Items: items, Loaded: true}), c
}

func TestPodmanScreen(t *testing.T) {
	p, c := podmanScreen(t)
	s := text(t, p, c)
	wantAll(t, s,
		"Recipes › ◆ Podman", "podman 5.3 · applehv",
		"● podman-machine-default", "running", "4 CPU · 4 GB", "disk on Mac", "of 100 GB",
		"Images 6", "Containers 3", "Volumes 2",
		"IMAGE", "ID", "SIZE", "CREATED", "USED BY",
		"[✓] localhost/indexer:build-2041", "3f9a1c07be52", "2.3 GB",
		"[✓] <none>:<none>", "dangling",
		" ○  localhost/api:dev", "api-dev · running",
		"[ ] docker.io/library/redis:7-alpine", "1 stopped container",
		"only after fstrim", "␣ mark", "a all unused", "plan")
	// Three unused images and one stopped container are selected to start with.
	wantAll(t, s, "4 items · 4.8 GB")

	// A locked image cannot be marked.
	var scr Screen = p
	for i := 0; i < 4; i++ { // past the four free images, onto api:dev
		scr = press(scr, "j")
	}
	scr = press(scr, " ")
	wantAll(t, text(t, scr, c), "In use by api-dev · running — it stays", "4 items · 4.8 GB")

	// The next section.
	p2, c2 := podmanScreen(t)
	scr = press(p2, "l")
	wantAll(t, text(t, scr, c2), "CONTAINER", "[✓] old-cache", " ○  api-dev", "running")
}

func TestPodmanScreenBuildsTheRecipePlan(t *testing.T) {
	p, c := podmanScreen(t)
	_, cmd := p.Update(pressMsg("enter"))
	if cmd == nil {
		t.Fatal("enter should open the plan")
	}
	msg, ok := cmd().(PushMsg)
	if !ok {
		t.Fatalf("got %T, want a PushMsg", cmd())
	}
	s := text(t, msg.Screen, c)
	wantAll(t, s, "Recipes › Podman › Plan", "Remove 1 container", "Remove 3 images",
		"Tell your Mac which blocks are free", "Stop the machine", "↓ 1 step more",
		"1 [✓] stop when done", "2 [ ] start again after", "machine disk on Mac")

	// Turning the switch off rebuilds the plan without the stop step.
	next := press(msg.Screen, "1")
	s = text(t, next, c)
	if strings.Contains(s, "Stop the machine") {
		t.Fatalf("the stop step should be gone:\n%s", s)
	}
	wantAll(t, s, "1 [ ] stop when done")
}

func TestRunScreen(t *testing.T) {
	c := testCtx(theme.LightTheme())
	plan := podmanPlan(c.Home())
	r := &Run{
		c: c, spec: PlanSpec{Plan: plan, Crumbs: []string{"Recipes", "Podman"}},
		eng:     c.NewEngine(nil),
		status:  []int{stepDone, stepDone, stepRun, stepWait, stepWait},
		results: make([]engine.StepResult, 5),
		cur:     2,
		start:   time.Now().Add(-17 * time.Second),
		tail: []string{
			"/var: 0 B (0 bytes) trimmed on /dev/vda4",
			"/boot: 0 B (0 bytes) trimmed on /dev/vda3",
			"/sysroot: 6.4 GiB (6881245184 bytes) trimmed on /dev/vda4",
		},
		metricLabel: "machine disk on Mac", metricBefore: 38_100_000_000, metricNow: 33_900_000_000,
	}
	r.results[0] = engine.StepResult{Ran: true, Elapsed: 4 * time.Second, Bytes: 6_400_000_000}
	r.results[1] = engine.StepResult{Ran: true, Elapsed: time.Second}

	s := text(t, r, c)
	wantAll(t, s, "Recipes › Podman › Running", "00:17", "step 3 of 5",
		"✓  Remove 6 images", "6.4 GB · 4 s", "·  Stop the machine",
		"/sysroot: 6.4 GiB (6881245184 bytes) trimmed on /dev/vda4",
		"machine disk on Mac", "38.1 GB", "33.9 GB", "and shrinking",
		"l full log", "ctrl+c stop after this step", "log: ~/.local/state/space/2026-11-22.log")

	cut := false
	r.cancel = func() { cut = true }
	next, _ := r.Interrupt()
	wantAll(t, text(t, next, c), "stopping after this step… again to cut it short")
	if !r.eng.Stopping() || cut {
		t.Fatal("the first ctrl+c asks the engine to stop after the step, and cuts nothing")
	}
	r.Interrupt()
	if !cut {
		t.Fatal("the second ctrl+c cuts the running step short")
	}
}

func TestDoneScreen(t *testing.T) {
	c := testCtx(theme.DarkTheme())
	c.Root = t.TempDir()
	plan := podmanPlan(c.Home())
	plan.Summary = []string{"images removed\t6", "api-dev, db-dev\tstopped with the machine"}
	plan.After = []recipe.Action{{Key: "s", Label: "start machine"}}
	spec := PlanSpec{Plan: plan, Crumbs: []string{"Recipes", "Podman"}}
	steps := make([]engine.StepResult, 5)
	for i := range steps {
		steps[i] = engine.StepResult{Ran: true}
	}

	ok := engine.Result{
		Plan: plan, Steps: steps, Failed: -1, Elapsed: 41 * time.Second,
		FreeBefore: 82_000_000_000, FreeAfter: 88_600_000_000,
		Metric: &recipe.Metric{Label: "machine disk on Mac", Before: 38_100_000_000, After: 31_500_000_000},
	}
	wantAll(t, text(t, NewDone(c, spec, ok), c),
		"Recipes › Podman › Done", "✓", "Reclaimed 6.6 GB", "5 steps · 41 s",
		"before", "82.0 GB free", "after", "88.6 GB free",
		"machine disk on Mac", "38.1 GB → 31.5 GB", "images removed", "stopped with the machine",
		"s start machine", "l log", "q quit", "back to overview")

	failed := ok
	failed.Failed, failed.Err = 2, errors.New("podman exited with status 125")
	failed.Steps = append([]engine.StepResult{}, steps...)
	failed.Steps[2].Err = failed.Err
	failed.Steps[3], failed.Steps[4] = engine.StepResult{}, engine.StepResult{}
	s := text(t, NewDone(c, spec, failed), c)
	wantAll(t, s, "✗", "Step 3 failed: Tell your Mac which blocks are free",
		"podman exited with status 125", "The 2 steps before it ran and stay done.")
	if strings.Contains(s, "start machine") {
		t.Fatal("follow-ups are not offered after a failure")
	}

	stopped := ok
	stopped.Stopped = true
	stopped.Steps = append([]engine.StepResult{}, steps...)
	stopped.Steps[3], stopped.Steps[4] = engine.StepResult{}, engine.StepResult{}
	wantAll(t, text(t, NewDone(c, spec, stopped), c), "Stopped after step 3 of 5")

	trashed := engine.Result{Plan: recipe.Plan{Title: "Remove"}, Steps: steps[:1], Failed: -1,
		FreeBefore: 82_000_000_000, FreeAfter: 82_000_000_000, Trashed: 20_500_000_000}
	wantAll(t, text(t, NewDone(c, PlanSpec{Plan: trashed.Plan}, trashed), c),
		"Moved 20.5 GB to the Trash", "Space comes back when the Trash is emptied", "e empty Trash")
}

func TestScreensSurviveNarrowWindows(t *testing.T) {
	p, c := podmanScreen(t)
	for _, size := range [][2]int{{80, 20}, {96, 24}, {160, 50}} {
		c.W, c.H = size[0], size[1]
		text(t, p, c)
		c2 := testCtx(theme.LightTheme())
		c2.W, c2.H = size[0], size[1]
		plan := planScreen(theme.LightTheme())
		plan.c = c2
		text(t, plan, c2)
	}
}

func TestDoneScreenListsWhatStayedBehind(t *testing.T) {
	c := testCtx(theme.DarkTheme())
	c.Root = t.TempDir()
	home := c.Home()
	permission := engine.Explain(&os.PathError{Op: "unlinkat", Path: "/x", Err: os.ErrPermission})
	plan := recipe.Plan{Title: "Remove", Steps: []recipe.Step{{Title: "Move 4 items to the Trash"}}}
	spec := PlanSpec{Plan: plan, Crumbs: []string{"~"}}

	// Some went, some stayed: two for one reason, one for another.
	partial := engine.Result{
		Plan: plan, Failed: -1, FreeBefore: 82e9, FreeAfter: 82e9, Trashed: 3e9,
		Steps: []engine.StepResult{{Ran: true, Bytes: 3e9, Failures: []engine.Failure{
			{Path: home + "/Library/Mail/V10/big.mbox", Reason: permission},
			{Path: home + "/Library/Messages/chat.db", Reason: permission},
			{Path: home + "/Movies/open.mov", Reason: "in use by another program"},
		}}},
	}
	s := text(t, NewDone(c, spec, partial), c)
	wantAll(t, s, "✓", "Moved 3.0 GB to the Trash", "3 items could not be removed",
		"~/Library/Mail/V10/big.mbox", "~/Library/Messages/chat.db",
		"macOS did not allow it. Give your terminal Full Disk Access", "or remove it in Finder",
		"~/Movies/open.mov — in use by another program", "e empty Trash")
	if strings.Count(s, "macOS did not allow it") != 1 {
		t.Fatalf("one explanation for the items that share it:\n%s", s)
	}

	// Nothing went at all.
	none := engine.Result{
		Plan: plan, Failed: -1, FreeBefore: 82e9, FreeAfter: 82e9,
		Steps: []engine.StepResult{{Ran: true, Failures: []engine.Failure{
			{Path: home + "/Movies/open.mov", Reason: "in use by another program"},
		}}},
	}
	s = text(t, NewDone(c, spec, none), c)
	wantAll(t, s, "✗", "Nothing was removed", "1 item could not be removed",
		"~/Movies/open.mov — in use by another program")
	if strings.Contains(s, "empty Trash") {
		t.Fatal("there is nothing in the Trash to empty")
	}

	// A long list is cut, with a pointer to the log.
	var many []engine.Failure
	for i := 0; i < 8; i++ {
		many = append(many, engine.Failure{Path: home + "/x/" + string(rune('a'+i)), Reason: "in use by another program"})
	}
	long := engine.Result{Plan: plan, Failed: -1, Steps: []engine.StepResult{{Ran: true, Failures: many}}}
	s = text(t, NewDone(c, spec, long), c)
	wantAll(t, s, "8 items could not be removed", "~/x/e", "+3 more — press l for the log")
	if strings.Contains(s, "~/x/f") {
		t.Fatal("only the first five are listed")
	}
}

func TestRunScreenMarksAStepThatLeftThingsBehind(t *testing.T) {
	c := testCtx(theme.DarkTheme())
	plan := recipe.Plan{Title: "Remove", Steps: []recipe.Step{
		{Title: "Move 3 items to the Trash", Remove: &recipe.Remove{Paths: []string{"/a"}, ToTrash: true}},
		{Title: "Delete 1 cache", Remove: &recipe.Remove{Paths: []string{"/b"}}},
	}}
	r := &Run{
		c: c, spec: PlanSpec{Plan: plan}, eng: c.NewEngine(nil),
		status:  []int{stepDone, stepRun},
		results: []engine.StepResult{{Ran: true, Bytes: 1e9, Failures: make([]engine.Failure, 2)}, {}},
		cur:     1, start: time.Now(),
	}
	wantAll(t, text(t, r, c), "!  Move 3 items to the Trash", "2 failed")
}

// The run screen reads engine events itself, once it is placed: the command
// that opens it must not read them too.
func TestRunScreenStartsReadingOnlyWhenPlaced(t *testing.T) {
	c := testCtx(theme.DarkTheme())
	c.Root = t.TempDir()
	c.Paths.StateDir = t.TempDir()
	plan := recipe.Plan{Title: "Quick", Steps: []recipe.Step{{Title: "nothing", Cmd: []string{"true"}}}}
	msg := runCmd(c, PlanSpec{Plan: plan})()
	rep, ok := msg.(ReplaceMsg)
	if !ok {
		t.Fatalf("opening the run screen yields %T, want only a ReplaceMsg", msg)
	}
	starter, ok := rep.Screen.(Starter)
	if !ok {
		t.Fatal("the run screen must start its own reading")
	}
	// Reading from Start, the screen sees the run through to the end.
	var s Screen = rep.Screen
	cmd := starter.Start()
	for i := 0; i < 20 && cmd != nil; i++ {
		m := cmd()
		if r, ok := m.(ReplaceMsg); ok {
			if _, done := r.Screen.(*Done); !done {
				t.Fatalf("the run should end on the done screen, got %T", r.Screen)
			}
			return
		}
		s, cmd = s.Update(m)
	}
	t.Fatal("the run never reached the done screen")
}
