//go:build darwin

package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/romus/space/pkg/recipe"
)

// fakeExec records commands instead of running them.
type fakeExec struct {
	ran    [][]string
	failOn string
	onRun  func(argv []string)
}

func (f *fakeExec) Exec(ctx context.Context, argv []string, out io.Writer) error {
	f.ran = append(f.ran, argv)
	fmt.Fprintf(out, "ran %s\nsecond line\n", argv[0])
	if f.onRun != nil {
		f.onRun(argv)
	}
	if f.failOn != "" && argv[0] == f.failOn {
		return errors.New("boom")
	}
	return nil
}

func home(t *testing.T) string {
	t.Helper()
	h, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func cmdPlan(names ...string) recipe.Plan {
	p := recipe.Plan{Title: "Test"}
	for _, n := range names {
		p.Steps = append(p.Steps, recipe.Step{Title: "run " + n, Cmd: []string{n, "arg"}})
	}
	return p
}

func TestStepsRunInOrderAndAreLogged(t *testing.T) {
	h := home(t)
	fx := &fakeExec{}
	e := &Engine{Home: h, Volume: h, Exec: fx, LogPath: filepath.Join(h, "state/run.log")}

	var events []string
	res := e.Run(context.Background(), cmdPlan("one", "two", "three"), func(ev Event) {
		switch ev := ev.(type) {
		case StepStarted:
			events = append(events, fmt.Sprintf("start %d", ev.Index))
		case StepOutput:
			events = append(events, "out "+ev.Line)
		case StepFinished:
			events = append(events, fmt.Sprintf("end %d", ev.Index))
		}
	})

	if res.Failed != -1 || res.Stopped || res.Done() != 3 {
		t.Fatalf("result: %+v", res)
	}
	if len(fx.ran) != 3 || fx.ran[0][0] != "one" || fx.ran[2][0] != "three" {
		t.Fatalf("order: %v", fx.ran)
	}
	want := "start 0|out ran one|out second line|end 0|start 1"
	if got := strings.Join(events[:5], "|"); got != want {
		t.Fatalf("events:\n got %s\nwant %s", got, want)
	}
	log, err := os.ReadFile(e.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"Test — 3 steps", "step 2: run two", "$ two arg", "ran three", "done in"} {
		if !strings.Contains(string(log), s) {
			t.Fatalf("log misses %q:\n%s", s, log)
		}
	}
	if res.FreeBefore <= 0 || res.FreeAfter <= 0 {
		t.Fatal("free space was not measured")
	}
}

func TestFailureStopsThePlan(t *testing.T) {
	h := home(t)
	fx := &fakeExec{failOn: "two"}
	e := &Engine{Home: h, Volume: h, Exec: fx}
	res := e.Run(context.Background(), cmdPlan("one", "two", "three"), nil)
	if res.Failed != 1 || res.Err == nil || len(fx.ran) != 2 {
		t.Fatalf("a failed step must stop the plan: failed=%d ran=%v", res.Failed, fx.ran)
	}
	if res.Steps[2].Ran {
		t.Fatal("step three must not run")
	}
}

func TestStopHappensBetweenSteps(t *testing.T) {
	h := home(t)
	fx := &fakeExec{}
	e := &Engine{Home: h, Volume: h, Exec: fx}
	// The user presses ctrl+c while step one is running.
	fx.onRun = func(argv []string) {
		if argv[0] == "one" {
			e.StopAfterStep()
		}
	}
	res := e.Run(context.Background(), cmdPlan("one", "two"), nil)
	if !res.Stopped || len(fx.ran) != 1 || res.Done() != 1 {
		t.Fatalf("step one must finish, step two must not start: %+v ran=%v", res, fx.ran)
	}
}

func TestMeasureBeforeAndAfter(t *testing.T) {
	h := home(t)
	n := int64(100)
	e := &Engine{Home: h, Volume: h, Exec: &fakeExec{onRun: func([]string) { n = 60 }},
		Measure: func(context.Context) (string, int64, bool) { return "disk", n, true }}
	res := e.Run(context.Background(), cmdPlan("shrink"), nil)
	if res.Metric == nil || res.Metric.Before != 100 || res.Metric.After != 60 {
		t.Fatalf("metric: %+v", res.Metric)
	}
}

func removePlan(root string, toTrash bool, paths ...string) recipe.Plan {
	p := recipe.Plan{Title: "Remove", Steps: []recipe.Step{{
		Title: "remove", Remove: &recipe.Remove{Paths: paths, ToTrash: toTrash, Root: root},
	}}}
	Prepare(&p)
	return p
}

func TestRemoveToTrashAndForGood(t *testing.T) {
	h := home(t)
	a, b := filepath.Join(h, "docs/a.txt"), filepath.Join(h, "docs/dir")
	touch(t, a)
	touch(t, filepath.Join(b, "inner.txt"))
	trashDir := filepath.Join(h, ".Trash")
	e := &Engine{Home: h, Volume: h, Trash: DirTrash{Dir: trashDir}}

	res := e.Run(context.Background(), removePlan(h, true, a, b), nil)
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if exists(a) || exists(b) {
		t.Fatal("trashed paths must be gone")
	}
	if !exists(filepath.Join(trashDir, "a.txt")) || !exists(filepath.Join(trashDir, "dir/inner.txt")) {
		t.Fatal("trashed paths must be in the Trash")
	}
	if res.Trashed <= 0 {
		t.Fatal("trashed bytes were not counted")
	}

	c := filepath.Join(h, "docs/c.txt")
	touch(t, c)
	res = e.Run(context.Background(), removePlan(h, false, c), nil)
	if res.Err != nil || exists(c) || exists(filepath.Join(trashDir, "c.txt")) {
		t.Fatalf("a permanent delete must not go through the Trash: %v", res.Err)
	}
}

func TestTrashKeepsBothWhenNamesCollide(t *testing.T) {
	h := home(t)
	one, two := filepath.Join(h, "x/same.txt"), filepath.Join(h, "y/same.txt")
	touch(t, one)
	touch(t, two)
	trashDir := filepath.Join(h, ".Trash")
	e := &Engine{Home: h, Volume: h, Trash: DirTrash{Dir: trashDir}}
	if res := e.Run(context.Background(), removePlan(h, true, one, two), nil); res.Err != nil {
		t.Fatal(res.Err)
	}
	entries, _ := os.ReadDir(trashDir)
	if len(entries) != 2 {
		t.Fatalf("both files must survive in the Trash, got %d entries", len(entries))
	}
}

func TestRemoveRefusesUnsafePaths(t *testing.T) {
	h := home(t)
	root := filepath.Join(h, "project")
	inside := filepath.Join(root, "ok.txt")
	outside := filepath.Join(h, "other/keep.txt")
	touch(t, inside)
	touch(t, outside)
	e := &Engine{Home: h, Volume: h, Trash: DirTrash{Dir: filepath.Join(h, ".Trash")}}

	cases := []struct {
		name  string
		root  string
		paths []string
	}{
		{"outside the root", root, []string{outside}},
		{"the root itself", root, []string{root}},
		{"the home directory", h, []string{h}},
		{"a parent of home", filepath.Dir(h), []string{h}},
		{"root is /", "/", []string{inside}},
		{"no root", "", []string{inside}},
		{"a relative path", root, []string{"ok.txt"}},
		{"dot-dot out of the root", root, []string{filepath.Join(root, "../other/keep.txt")}},
	}
	for _, c := range cases {
		res := e.Run(context.Background(), removePlan(c.root, false, c.paths...), nil)
		// A root that is not allowed fails the step; a path that is not
		// allowed is left behind and reported. Either way nothing goes.
		if res.Err == nil && len(res.Failures()) == 0 {
			t.Errorf("%s: should be refused", c.name)
		}
		if !exists(inside) || !exists(outside) {
			t.Fatalf("%s: a file was removed", c.name)
		}
	}
}

// One path that cannot be removed does not hold back the others, and the
// reason is given in plain words.
func TestOneFailureDoesNotStopTheRest(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root removes everything")
	}
	h := home(t)
	first := filepath.Join(h, "docs/first.txt")
	stuck := filepath.Join(h, "docs/locked/stuck.txt")
	last := filepath.Join(h, "docs/last.txt")
	cache := filepath.Join(h, "docs/cache")
	for _, p := range []string{first, stuck, last, filepath.Join(cache, "blob")} {
		touch(t, p)
	}
	// Nothing can be renamed out of, or deleted from, a read-only folder.
	locked := filepath.Dir(stuck)
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(locked, 0o755)

	for _, toTrash := range []bool{true, false} {
		touch(t, first)
		touch(t, last)
		touch(t, filepath.Join(cache, "blob"))
		plan := recipe.Plan{Title: "Remove", Steps: []recipe.Step{
			{Title: "files", Remove: &recipe.Remove{Paths: []string{first, stuck, last}, ToTrash: toTrash, Root: h}},
			{Title: "caches", Remove: &recipe.Remove{Paths: []string{cache}, Root: h}},
		}}
		Prepare(&plan)
		e := &Engine{Home: h, Volume: h, Trash: DirTrash{Dir: filepath.Join(h, ".Trash")},
			LogPath: filepath.Join(h, "state/run.log")}
		var lines []string
		res := e.Run(context.Background(), plan, func(ev Event) {
			if o, ok := ev.(StepOutput); ok {
				lines = append(lines, o.Line)
			}
		})

		if res.Err != nil || res.Failed != -1 {
			t.Fatalf("toTrash=%v: a path that stays behind is not a failed step: %v", toTrash, res.Err)
		}
		if exists(first) || exists(last) {
			t.Fatalf("toTrash=%v: the paths around the stuck one must still go", toTrash)
		}
		if !exists(stuck) {
			t.Fatalf("toTrash=%v: the stuck file cannot have gone", toTrash)
		}
		if exists(cache) {
			t.Fatalf("toTrash=%v: the next step must still run", toTrash)
		}
		fails := res.Failures()
		if len(fails) != 1 || fails[0].Path != stuck {
			t.Fatalf("toTrash=%v: failures: %+v", toTrash, fails)
		}
		if !strings.Contains(fails[0].Reason, "macOS did not allow it") || !strings.Contains(fails[0].Reason, "Full Disk Access") {
			t.Fatalf("toTrash=%v: the reason should say what to do: %q", toTrash, fails[0].Reason)
		}
		if len(res.Steps[0].Failures) != 1 || len(res.Steps[1].Failures) != 0 || res.Steps[0].Bytes <= 0 {
			t.Fatalf("toTrash=%v: step results: %+v", toTrash, res.Steps)
		}
		want := "could not remove ~/docs/locked/stuck.txt: macOS did not allow it"
		found := false
		for _, l := range lines {
			found = found || strings.HasPrefix(l, want)
		}
		log, _ := os.ReadFile(e.LogPath)
		if !found || !strings.Contains(string(log), want) {
			t.Fatalf("toTrash=%v: the failure must be in the output and the log:\n%s", toTrash, strings.Join(lines, "\n"))
		}
	}
}

func TestEveryPathFailing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root removes everything")
	}
	h := home(t)
	a, b := filepath.Join(h, "locked/a.txt"), filepath.Join(h, "locked/b.txt")
	touch(t, a)
	touch(t, b)
	if err := os.Chmod(filepath.Dir(a), 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Dir(a), 0o755)

	e := &Engine{Home: h, Volume: h, Trash: DirTrash{Dir: filepath.Join(h, ".Trash")}}
	res := e.Run(context.Background(), removePlan(h, true, a, b), nil)
	if len(res.Failures()) != 2 || res.Trashed != 0 || res.Steps[0].Bytes != 0 {
		t.Fatalf("both must be reported and nothing counted: %+v", res.Steps[0])
	}
	if !exists(a) || !exists(b) {
		t.Fatal("nothing can have gone")
	}
}

func TestExplain(t *testing.T) {
	permission := "macOS did not allow it. Give your terminal Full Disk Access in " +
		"System Settings › Privacy & Security, or remove it in Finder"
	cases := []struct {
		err  error
		want string
	}{
		{&fs.PathError{Op: "unlinkat", Path: "/x/y", Err: syscall.EPERM}, permission},
		{&os.LinkError{Op: "rename", Old: "/x/y", New: "/t/y", Err: syscall.EACCES}, permission},
		{&fs.PathError{Op: "unlinkat", Path: "/x/y", Err: syscall.EBUSY}, "in use by another program"},
		{&fs.PathError{Op: "unlinkat", Path: "/x/y", Err: syscall.EROFS}, "the disk is read-only"},
		{&fs.PathError{Op: "unlinkat", Path: "/x/y", Err: syscall.ENOTEMPTY}, "directory not empty"},
		{ErrUnsafe, "refused: outside the allowed folder"},
		{fmt.Errorf("%w: no root", ErrUnsafe), "refused: outside the allowed folder: no root"},
		{ErrChanged, "refused: changed since the plan was made"},
		{errors.New("could not move to the Trash: the volume has no Trash"), "could not move to the Trash: the volume has no Trash"},
	}
	for _, c := range cases {
		if got := Explain(c.err); got != c.want {
			t.Errorf("Explain(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

func TestRemoveRefusesThroughASymlink(t *testing.T) {
	h := home(t)
	root := filepath.Join(h, "project")
	secret := filepath.Join(h, "secret/key.txt")
	touch(t, secret)
	touch(t, filepath.Join(root, "file.txt"))
	// project/escape → ../secret: a path under it leaves the root.
	if err := os.Symlink(filepath.Join(h, "secret"), filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	e := &Engine{Home: h, Volume: h}
	res := e.Run(context.Background(), removePlan(root, false, filepath.Join(root, "escape/key.txt")), nil)
	fails := res.Failures()
	if len(fails) != 1 || !errors.Is(fails[0].Err, ErrUnsafe) || !exists(secret) {
		t.Fatalf("a path reached through a symlink must be refused: %+v", fails)
	}
	if fails[0].Reason != "refused: outside the allowed folder" {
		t.Fatalf("reason: %q", fails[0].Reason)
	}
	// The link itself may be removed; its target stays.
	res = e.Run(context.Background(), removePlan(root, false, filepath.Join(root, "escape")), nil)
	if res.Err != nil || exists(filepath.Join(root, "escape")) || !exists(secret) {
		t.Fatalf("removing a symlink must remove the link only: %v", res.Err)
	}
}

func TestRemoveRefusesAReplacedFile(t *testing.T) {
	h := home(t)
	path := filepath.Join(h, "docs/report.txt")
	touch(t, path)
	plan := removePlan(h, false, path) // the plan is shown now
	// Something replaces the file before the user presses enter.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	touch(t, filepath.Join(h, "docs/other.txt")) // takes the freed inode on most filesystems
	touch(t, path)
	if plan.Steps[0].Remove.Inodes[0] == inode(t, path) {
		t.Skip("the filesystem reused the inode")
	}
	e := &Engine{Home: h, Volume: h}
	res := e.Run(context.Background(), plan, nil)
	fails := res.Failures()
	if len(fails) != 1 || !errors.Is(fails[0].Err, ErrChanged) || !exists(path) {
		t.Fatalf("a file that changed since the plan must be refused: %+v", fails)
	}
	if fails[0].Reason != "refused: changed since the plan was made" {
		t.Fatalf("reason: %q", fails[0].Reason)
	}
}

func inode(t *testing.T, path string) uint64 {
	t.Helper()
	p := recipe.Plan{Steps: []recipe.Step{{Remove: &recipe.Remove{Paths: []string{path}}}}}
	Prepare(&p)
	return p.Steps[0].Remove.Inodes[0]
}

func TestMissingPathsAreSkipped(t *testing.T) {
	h := home(t)
	path := filepath.Join(h, "docs/a.txt")
	touch(t, path)
	e := &Engine{Home: h, Volume: h}
	var lines []string
	res := e.Run(context.Background(), removePlan(h, false, filepath.Join(h, "docs/gone.txt"), path), func(ev Event) {
		if o, ok := ev.(StepOutput); ok {
			lines = append(lines, o.Line)
		}
	})
	if res.Err != nil || exists(path) {
		t.Fatalf("a path that is already gone is not an error: %v", res.Err)
	}
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "already gone: ~/docs/gone.txt") {
		t.Fatalf("output: %q", lines)
	}
}

func TestScriptAndDisplay(t *testing.T) {
	plan := recipe.Plan{Title: "Podman", Steps: []recipe.Step{
		{Title: "Remove 2 images", Cmd: []string{"podman", "rmi", "3f9a1c07be52", "9e04d7aa1f30"}, Why: "Images no container is using."},
		{Title: "Tell your Mac which blocks are free", Cmd: []string{"podman", "machine", "ssh", "--", "sudo", "fstrim", "-av"}, Safe: true},
		{Title: "Move 2 items to the Trash", Reversible: true,
			Remove: &recipe.Remove{Paths: []string{"/Users/me/My Movie.mov", "/Users/me/it's.zip"}, ToTrash: true, Root: "/Users/me"}},
		{Title: "Delete 1 cache", Safe: true, Remove: &recipe.Remove{Paths: []string{"/Users/me/.npm"}, Root: "/Users/me"}},
	}}
	got := Script(plan, time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC))
	want := `#!/bin/sh
# space plan: Podman — 4 steps
# Generated on 2026-11-22. Read it before you run it.
set -eu

# 1. Remove 2 images — IRREVERSIBLE
#    Images no container is using.
podman rmi 3f9a1c07be52 9e04d7aa1f30

# 2. Tell your Mac which blocks are free — SAFE
podman machine ssh -- sudo fstrim -av

# 3. Move 2 items to the Trash — REVERSIBLE
trash \
  '/Users/me/My Movie.mov' \
  '/Users/me/it'\''s.zip'

# 4. Delete 1 cache — SAFE
rm -rf -- /Users/me/.npm
`
	if got != want {
		t.Fatalf("script:\n%s\nwant:\n%s", got, want)
	}

	if d := Display(plan.Steps[2], "/Users/me"); d != `trash '~/My Movie.mov' '~/it'\''s.zip'` {
		t.Fatalf("display: %s", d)
	}
	many := recipe.Step{Remove: &recipe.Remove{Paths: []string{"/a", "/b", "/c", "/d", "/e"}}}
	if d := Display(many, ""); d != "rm -rf -- /a /b /c … +2 more" {
		t.Fatalf("a long list is cut: %s", d)
	}
}

// A step that will not end can be cut short: the run stops at once and is
// reported as stopped by the user, not as broken.
func TestCancelCutsARunningStepShort(t *testing.T) {
	h := home(t)
	e := &Engine{Home: h, Volume: h}
	ctx, cancel := context.WithCancel(context.Background())
	plan := recipe.Plan{Title: "Slow", Steps: []recipe.Step{
		{Title: "wait", Cmd: []string{"sleep", "30"}},
		{Title: "never", Cmd: []string{"true"}},
	}}
	start := time.Now()
	res := e.Run(ctx, plan, func(ev Event) {
		if _, ok := ev.(StepStarted); ok {
			go func() {
				time.Sleep(100 * time.Millisecond)
				cancel()
			}()
		}
	})
	if time.Since(start) > 10*time.Second {
		t.Fatal("the step was not cut short")
	}
	if !res.Stopped || res.Failed != -1 || res.Steps[1].Ran {
		t.Fatalf("want a stopped run with step two untouched: %+v", res)
	}
}
