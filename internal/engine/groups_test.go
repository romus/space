//go:build darwin

package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/romus/space/pkg/recipe"
)

// groupExec records commands that may run side by side.
type groupExec struct {
	mu            sync.Mutex
	ran           []string
	running, peak int
	fail          map[string]bool
	gate          chan struct{} // when set, every command waits for it to close
}

func (f *groupExec) Exec(ctx context.Context, argv []string, out io.Writer) error {
	f.mu.Lock()
	f.ran = append(f.ran, argv[0])
	f.running++
	f.peak = max(f.peak, f.running)
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.running--
		f.mu.Unlock()
	}()
	fmt.Fprintf(out, "ran %s\n", argv[0])
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if f.fail[argv[0]] {
		return errors.New("boom")
	}
	return nil
}

func (f *groupExec) names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.ran...)
}

// waitRunning blocks until n commands are running at once.
func (f *groupExec) waitRunning(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		running := f.running
		f.mu.Unlock()
		if running == n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("never saw %d commands running at once", n)
}

// record collects events as text. The engine never calls emit from two
// goroutines, so it needs no lock of its own.
func record(events *[]string) func(Event) {
	return func(ev Event) {
		switch ev := ev.(type) {
		case StepStarted:
			*events = append(*events, fmt.Sprintf("start %d", ev.Index))
		case StepOutput:
			*events = append(*events, fmt.Sprintf("out %d", ev.Index))
		case StepFinished:
			*events = append(*events, fmt.Sprintf("end %d", ev.Index))
		case GroupFinished:
			*events = append(*events, "group "+ev.Result.Name)
		}
	}
}

func TestGroupStepsRunInOrder(t *testing.T) {
	h := home(t)
	fx := &groupExec{}
	e := &Engine{Home: h, Volume: h, Exec: fx}
	plan := groupPlan(4, "a:a1", "b:b1", "c:c1", "a:a2", "b:b2", "c:c2", "a:a3", "b:b3", "c:c3")

	var events []string
	res := e.Run(context.Background(), plan, record(&events))

	if res.Failed != -1 || res.Stopped || res.Done() != 9 || res.FinishedGroups() != 3 {
		t.Fatalf("result: %+v", res)
	}
	// Inside a group the steps follow one another, whatever the others do.
	at := map[string]int{}
	for i, name := range fx.names() {
		at[name] = i
	}
	for _, g := range []string{"a", "b", "c"} {
		if !(at[g+"1"] < at[g+"2"] && at[g+"2"] < at[g+"3"]) {
			t.Fatalf("group %s ran out of order: %v", g, fx.names())
		}
	}
	// Every step reports start, output, end, in that order.
	pos := map[string]int{}
	for i, ev := range events {
		pos[ev] = i
	}
	for i := range plan.Steps {
		s, o, f := pos[fmt.Sprintf("start %d", i)], pos[fmt.Sprintf("out %d", i)], pos[fmt.Sprintf("end %d", i)]
		if !(s < o && o < f) {
			t.Fatalf("step %d events out of order: %v", i, events)
		}
	}
	for _, g := range res.Groups {
		if g.Done != 3 || g.Elapsed <= 0 {
			t.Fatalf("group %s: %+v", g.Name, g)
		}
	}
}

func TestGroupsRunFourAtATime(t *testing.T) {
	h := home(t)
	fx := &groupExec{gate: make(chan struct{})}
	e := &Engine{Home: h, Volume: h, Exec: fx}
	var spec []string
	for i := range 10 {
		spec = append(spec, fmt.Sprintf("g%d:c%d", i, i))
	}
	done := make(chan Result, 1)
	go func() { done <- e.Run(context.Background(), groupPlan(4, spec...), nil) }()

	fx.waitRunning(t, 4)
	time.Sleep(20 * time.Millisecond) // a fifth would have started by now
	close(fx.gate)
	res := <-done
	if fx.peak != 4 {
		t.Fatalf("at most 4 groups at once, saw %d", fx.peak)
	}
	if res.FinishedGroups() != 10 {
		t.Fatalf("every group must finish: %+v", res.Groups)
	}
}

func TestOneGroupAtATimeIsInPlanOrder(t *testing.T) {
	h := home(t)
	e := &Engine{Home: h, Volume: h, Exec: &groupExec{}}
	var events []string
	e.Run(context.Background(), groupPlan(0, ":pre", "a:a1", "b:b1", "a:a2", ":post"), record(&events))
	want := "start 0|out 0|end 0|" +
		"start 1|out 1|end 1|start 3|out 3|end 3|group a|" +
		"start 2|out 2|end 2|group b|" +
		"start 4|out 4|end 4"
	if got := strings.Join(events, "|"); got != want {
		t.Fatalf("events:\n got %s\nwant %s", got, want)
	}
}

func TestAGroupFailureEndsOnlyThatGroup(t *testing.T) {
	h := home(t)
	fx := &groupExec{fail: map[string]bool{"b2": true}}
	e := &Engine{Home: h, Volume: h, Exec: fx}
	plan := groupPlan(4, ":pre", "a:a1", "a:a2", "b:b1", "b:b2", "b:b3", "c:c1", ":post")
	res := e.Run(context.Background(), plan, nil)

	if res.Failed != -1 || res.Err != nil || res.Stopped {
		t.Fatalf("a group failure does not end the run: %+v", res)
	}
	b := res.Groups[1]
	if b.Name != "b" || b.Failed != 4 || b.Err == nil || b.Done != 1 || b.Finished() {
		t.Fatalf("group b: %+v", b)
	}
	if res.Steps[5].Ran {
		t.Fatal("the step after the failed one must not run")
	}
	if got := strings.Join(res.Steps[4].Tail, "|"); got != "ran b2" {
		t.Fatalf("the failed step keeps what it said: %q", got)
	}
	if !res.Groups[0].Finished() || !res.Groups[2].Finished() {
		t.Fatalf("the other groups finish: %+v", res.Groups)
	}
	if !res.Steps[7].Ran {
		t.Fatal("the step after the groups still runs")
	}
	if f := res.FailedGroups(); len(f) != 1 || f[0].Name != "b" {
		t.Fatalf("failed groups: %+v", f)
	}
}

func TestAFailureBeforeTheGroupsEndsTheRun(t *testing.T) {
	h := home(t)
	fx := &groupExec{fail: map[string]bool{"pre": true}}
	e := &Engine{Home: h, Volume: h, Exec: fx}
	res := e.Run(context.Background(), groupPlan(4, ":pre", "a:a1", "b:b1"), nil)
	if res.Failed != 0 || len(fx.names()) != 1 {
		t.Fatalf("no group may start after a failed step: failed=%d ran=%v", res.Failed, fx.names())
	}
}

func TestAGroupThatFailedStaysStopped(t *testing.T) {
	h := home(t)
	fx := &groupExec{fail: map[string]bool{"backup": true}}
	e := &Engine{Home: h, Volume: h, Exec: fx}
	res := e.Run(context.Background(), groupPlan(4, "a:backup", "b:b1", ":mid", "a:compact", "b:b2"), nil)
	if got := strings.Join(fx.names(), " "); strings.Contains(got, "compact") {
		t.Fatalf("a group must not go on after it failed: %s", got)
	}
	later := res.Groups[2]
	if later.Name != "a" || later.Failed != 3 || later.Err == nil || res.Steps[3].Ran {
		t.Fatalf("the later part of group a: %+v", later)
	}
	if !res.Groups[3].Finished() {
		t.Fatalf("group b goes on: %+v", res.Groups[3])
	}
}

func TestStopLetsRunningStepsFinish(t *testing.T) {
	h := home(t)
	fx := &groupExec{gate: make(chan struct{})}
	e := &Engine{Home: h, Volume: h, Exec: fx}
	plan := groupPlan(2, "a:a1", "a:a2", "b:b1", "b:b2", "c:c1", "d:d1", ":post")
	done := make(chan Result, 1)
	go func() { done <- e.Run(context.Background(), plan, nil) }()

	fx.waitRunning(t, 2)
	e.StopAfterStep()
	close(fx.gate)
	res := <-done

	ran := fx.names()
	if len(ran) != 2 {
		t.Fatalf("the two running steps finish and nothing else starts: %v", ran)
	}
	if !res.Stopped || res.Failed != -1 || len(res.FailedGroups()) != 0 || res.FinishedGroups() != 0 {
		t.Fatalf("want a stopped run with no failures: %+v", res)
	}
	if res.Done() != 2 || res.Steps[6].Ran {
		t.Fatalf("steps done: %d, post ran: %v", res.Done(), res.Steps[6].Ran)
	}
}

// Commands that will not end are cut short together, and the groups are
// reported as stopped by the user, not as broken.
func TestCancelCutsRunningGroupsShort(t *testing.T) {
	h := home(t)
	e := &Engine{Home: h, Volume: h}
	ctx, cancel := context.WithCancel(context.Background())
	plan := recipe.Plan{Title: "Slow", Parallel: 4}
	for _, g := range []string{"a", "b", "c", "d"} {
		plan.Steps = append(plan.Steps,
			recipe.Step{Title: "wait", Cmd: []string{"sleep", "30"}, Group: g},
			recipe.Step{Title: "never", Cmd: []string{"true"}, Group: g})
	}
	var once sync.Once
	start := time.Now()
	res := e.Run(ctx, plan, func(ev Event) {
		if _, ok := ev.(StepStarted); ok {
			once.Do(func() {
				go func() {
					time.Sleep(200 * time.Millisecond)
					cancel()
				}()
			})
		}
	})
	if time.Since(start) > 10*time.Second {
		t.Fatal("the steps were not cut short")
	}
	if !res.Stopped || res.Failed != -1 || len(res.FailedGroups()) != 0 || res.Done() != 0 {
		t.Fatalf("want a stopped run and no failed group: %+v", res)
	}
}

func TestSudoInsideAGroupIsRefused(t *testing.T) {
	h := home(t)
	fx := &groupExec{}
	asked := false
	e := &Engine{Home: h, Volume: h, Exec: fx, Interactive: func(*exec.Cmd) error { asked = true; return nil }}
	plan := groupPlan(4, "a:a1", "a:a2", "b:b1")
	plan.Steps[1].Sudo = true
	res := e.Run(context.Background(), plan, nil)

	if asked {
		t.Fatal("a group must never ask for the terminal")
	}
	a := res.Groups[0]
	if a.Failed != 1 || a.Err == nil || a.Done != 0 || res.Steps[0].Ran {
		t.Fatalf("group a must be refused before its first step: %+v", a)
	}
	if !res.Groups[1].Finished() {
		t.Fatalf("group b is not affected: %+v", res.Groups[1])
	}
}

// Four groups move files with the same name to the Trash at once: every
// one of them must still be there afterwards.
func TestGroupsTrashTheSameNameSafely(t *testing.T) {
	h := home(t)
	trashDir := filepath.Join(h, ".Trash")
	e := &Engine{Home: h, Volume: h, Trash: DirTrash{Dir: trashDir}}
	plan := recipe.Plan{Title: "Trash", Parallel: 4}
	const groups, each = 4, 5
	for g := range groups {
		for i := range each {
			path := filepath.Join(h, fmt.Sprintf("repo%d/wt%d/same.txt", g, i))
			touch(t, path)
			plan.Steps = append(plan.Steps, recipe.Step{
				Title: "trash", Group: fmt.Sprintf("repo%d", g),
				Remove: &recipe.Remove{Paths: []string{path}, ToTrash: true, Root: filepath.Dir(path)},
			})
		}
	}
	Prepare(&plan)
	res := e.Run(context.Background(), plan, nil)

	if n := len(res.Failures()); n != 0 || res.FinishedGroups() != groups {
		t.Fatalf("failures: %d, groups: %+v", n, res.Groups)
	}
	entries, err := os.ReadDir(trashDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != groups*each {
		t.Fatalf("the Trash holds %d files, want %d: one overwrote another", len(entries), groups*each)
	}
	var sum int64
	for _, s := range res.Steps {
		sum += s.Bytes
	}
	if res.Trashed != sum || sum <= 0 {
		t.Fatalf("trashed %d, steps add up to %d", res.Trashed, sum)
	}
}

func TestRemoveInsideAGroupRefusesAReplacedFile(t *testing.T) {
	h := home(t)
	path := filepath.Join(h, "repo/wt/file.txt")
	touch(t, path)
	plan := recipe.Plan{Title: "Remove", Parallel: 4, Steps: []recipe.Step{{
		Title: "remove", Group: "repo",
		Remove: &recipe.Remove{Paths: []string{path}, Root: filepath.Join(h, "repo")},
	}}}
	Prepare(&plan)
	// The file is replaced after the plan was shown.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	touch(t, path+".tmp")
	touch(t, path)
	res := (&Engine{Home: h, Volume: h}).Run(context.Background(), plan, nil)
	if !exists(path) || len(res.Failures()) != 1 || !errors.Is(res.Failures()[0].Err, ErrChanged) {
		t.Fatalf("a replaced file must be refused: %+v", res.Failures())
	}
}

func TestGroupLog(t *testing.T) {
	h := home(t)
	fx := &groupExec{fail: map[string]bool{"b1": true}}
	e := &Engine{Home: h, Volume: h, Exec: fx, LogPath: filepath.Join(h, "state/run.log")}
	plan := groupPlan(1, ":pre", "web:a1", "indexer:b1", "indexer:b2")
	plan.GroupNoun = "repo"
	e.Run(context.Background(), plan, nil)

	data, err := os.ReadFile(e.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	// Without the first and last line, which carry the time and free space.
	got := strings.Join(lines[1:len(lines)-1], "\n")
	want := `--- step 1: run pre
$ pre arg
ran pre
web     | --- step 2: run a1
web     | $ a1 arg
web     | ran a1
web     | --- finished in 0 s
indexer | --- step 3: run b1
indexer | $ b1 arg
indexer | ran b1
indexer | !!! step 3 failed: boom
indexer | --- not finished: 2 steps left
--- 2 repos: 1 finished, 1 failed
!!! indexer: step 3: boom`
	if got != want {
		t.Fatalf("log:\n%s\nwant:\n%s", got, want)
	}
	if !strings.HasSuffix(lines[0], "Test — 4 steps in 2 repos, 1 at a time") {
		t.Fatalf("first line: %s", lines[0])
	}
}

func TestEmitIsNeverCalledByTwoGroupsAtOnce(t *testing.T) {
	h := home(t)
	e := &Engine{Home: h, Volume: h, Exec: &groupExec{}}
	var spec []string
	for g := range 8 {
		for i := range 20 {
			spec = append(spec, fmt.Sprintf("g%d:c%d", g, i))
		}
	}
	var inside, clashes atomic.Int32
	e.Run(context.Background(), groupPlan(4, spec...), func(Event) {
		if inside.Add(1) > 1 {
			clashes.Add(1)
		}
		time.Sleep(50 * time.Microsecond)
		inside.Add(-1)
	})
	if n := clashes.Load(); n > 0 {
		t.Fatalf("emit ran in two goroutines at once %d times", n)
	}
}
