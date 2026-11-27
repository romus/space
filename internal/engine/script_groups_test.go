//go:build darwin

package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/romus/space/pkg/recipe"
)

func TestScriptWithGroups(t *testing.T) {
	plan := recipe.Plan{Title: "Git", Parallel: 4, GroupNoun: "repo", Steps: []recipe.Step{
		{Title: "Create the backup folder", Cmd: []string{"mkdir", "-p", "/Users/me/.local/state/space/git"}, Safe: true},
		{Title: "Back up 2 branches", Group: "web", Safe: true, Why: "One file, kept 30 days.",
			Cmd: []string{"git", "-C", "/Users/me/work/web", "bundle", "create", "/tmp/web.bundle", "a", "b"}},
		{Title: "Delete 2 branches", Group: "web", Reversible: true,
			Cmd: []string{"git", "-C", "/Users/me/work/web", "branch", "-D", "a", "b"}},
		{Title: "Move 2 worktrees to the Trash", Group: "my api", Reversible: true,
			Remove: &recipe.Remove{Paths: []string{"/Users/me/work/api-1", "/Users/me/work/api 2"}, ToTrash: true, Root: "/Users/me/work"}},
		{Title: "Compact", Group: "my api", Safe: true, Cmd: []string{"git", "-C", "/Users/me/work/my api", "gc", "--prune=now"}},
	}}
	got := Script(plan, time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC))
	want := `#!/bin/sh
# space plan: Git — 5 steps in 2 repos
# Generated on 2026-11-27. Read it before you run it.
# Here each repo is done in turn; space itself does 4 at a time.
set -eu
failed=

# 1. Create the backup folder — SAFE
mkdir -p /Users/me/.local/state/space/git

# ── web ──
{
  # 2. Back up 2 branches — SAFE
  #    One file, kept 30 days.
  git -C /Users/me/work/web bundle create /tmp/web.bundle a b &&
  # 3. Delete 2 branches — REVERSIBLE
  git -C /Users/me/work/web branch -D a b
} || failed="$failed "web

# ── my api ──
{
  # 4. Move 2 worktrees to the Trash — REVERSIBLE
  trash \
    /Users/me/work/api-1 \
    '/Users/me/work/api 2' &&
  # 5. Compact — SAFE
  git -C '/Users/me/work/my api' gc --prune=now
} || failed="$failed "'my api'

[ -z "$failed" ] || { echo "not finished:$failed" >&2; exit 1; }
`
	if got != want {
		t.Fatalf("script:\n%s\nwant:\n%s", got, want)
	}
}

// The script is run for real, with harmless commands: a group whose command
// fails stops there, the others and the steps after them still run, and the
// script ends with an error that names the group.
func TestScriptWithGroupsBehavesLikeTheEngine(t *testing.T) {
	dir := t.TempDir()
	mark := func(name string) []string { return []string{"touch", filepath.Join(dir, name)} }
	plan := recipe.Plan{Title: "Test", Parallel: 4, Steps: []recipe.Step{
		{Title: "before", Cmd: mark("before")},
		{Title: "a one", Group: "a", Cmd: mark("a1")},
		{Title: "a breaks", Group: "a", Cmd: []string{"false"}},
		{Title: "a three", Group: "a", Cmd: mark("a3")},
		{Title: "b one", Group: "b", Cmd: mark("b1")},
		{Title: "b two", Group: "b", Cmd: mark("b2")},
		{Title: "after", Cmd: mark("after")},
	}}
	path := filepath.Join(dir, "plan.sh")
	if err := os.WriteFile(path, []byte(Script(plan, time.Now())), 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("/bin/sh", path).CombinedOutput()
	if err == nil {
		t.Fatalf("the script must fail when a group did not finish:\n%s", out)
	}
	if !strings.Contains(string(out), "not finished: a") {
		t.Fatalf("the script must name the group: %s", out)
	}
	for name, want := range map[string]bool{"before": true, "a1": true, "a3": false, "b1": true, "b2": true, "after": true} {
		if got := exists(filepath.Join(dir, name)); got != want {
			t.Errorf("%s: ran=%v, want %v", name, got, want)
		}
	}
}

// A name with a line break must not turn into a command of its own.
func TestScriptKeepsNamesOnOneLine(t *testing.T) {
	plan := recipe.Plan{Title: "Test", Steps: []recipe.Step{
		{Title: "clean\nrm -rf /", Why: "because\nreasons", Group: "x\ny", Cmd: []string{"true"}},
	}}
	for _, line := range strings.Split(Script(plan, time.Now()), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "rm ") || line == "y ──" || line == "reasons" {
			t.Fatalf("a name broke out of its comment: %q", line)
		}
	}
}
