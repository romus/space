//go:build darwin

package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
		if res.Err == nil {
			t.Errorf("%s: should be refused", c.name)
		}
		if !exists(inside) || !exists(outside) {
			t.Fatalf("%s: a file was removed", c.name)
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
	if !errors.Is(res.Err, ErrUnsafe) || !exists(secret) {
		t.Fatalf("a path reached through a symlink must be refused: %v", res.Err)
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
	if !errors.Is(res.Err, ErrChanged) || !exists(path) {
		t.Fatalf("a file that changed since the plan must be refused: %v", res.Err)
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
