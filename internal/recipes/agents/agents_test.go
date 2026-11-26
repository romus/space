//go:build darwin

package agents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/romus/space/pkg/recipe"
)

type noRunner struct{}

func (noRunner) Output(context.Context, string, ...string) ([]byte, error) {
	return nil, errors.New("no process")
}
func (noRunner) LookPath(string) (string, error) { return "", errors.New("not found") }

func put(t *testing.T, path, content string, daysOld int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-time.Duration(daysOld) * 24 * time.Hour)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func env(home string) recipe.Env {
	return recipe.Env{Home: home, Run: noRunner{}, Now: time.Now}
}

func claudeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	base := filepath.Join(home, ".claude")
	line := func(cwd string) string {
		return `{"type":"mode"}` + "\n" + `{"type":"user","cwd":"` + cwd + `","message":"hi"}` + "\n"
	}
	api := filepath.Join(base, "projects/-Users-me-work-api")
	put(t, filepath.Join(api, "old.jsonl"), line("/Users/me/work/api"), 90)
	put(t, filepath.Join(api, "old/subagents/agent.jsonl"), "sub-agent log", 90)
	put(t, filepath.Join(base, "file-history/old/checkpoint"), "checkpoint", 90)
	put(t, filepath.Join(api, "mid.jsonl"), line("/Users/me/work/api"), 40)
	put(t, filepath.Join(api, "new.jsonl"), line("/Users/me/work/api"), 2)
	put(t, filepath.Join(api, "memory/MEMORY.md"), "remember", 90)
	put(t, filepath.Join(base, "projects/-Users-me-thesis/only.jsonl"), line("/Users/me/thesis"), 60)
	put(t, filepath.Join(base, "settings.json"), "{}", 90)
	put(t, filepath.Join(base, "CLAUDE.md"), "# rules", 90)
	put(t, filepath.Join(base, "skills/x/SKILL.md"), "skill", 90)
	put(t, filepath.Join(base, "plans/plan.md"), "a plan", 90)
	put(t, filepath.Join(base, "shell-snapshots/old.sh"), "snapshot", 80)
	put(t, filepath.Join(base, "shell-snapshots/new.sh"), "snapshot", 1)
	return home
}

func inspect(t *testing.T, r recipe.Recipe, home string) []recipe.Item {
	t.Helper()
	if !r.Detect(context.Background(), env(home)) {
		t.Fatal("not detected")
	}
	items, err := r.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func find(items []recipe.Item, id string) *recipe.Item {
	for i := range items {
		if items[i].ID == id {
			return &items[i]
		}
	}
	return nil
}

func TestClaudeInspectOffersOnlyTheAllowList(t *testing.T) {
	home := claudeHome(t)
	items := inspect(t, &Claude{}, home)

	for _, it := range items {
		for _, p := range append([]string{it.Path}, companions(it)...) {
			for _, never := range []string{"memory", "settings.json", "CLAUDE.md", "/skills/", "/plans/"} {
				if strings.Contains(p, never) {
					t.Fatalf("%s must never be offered", p)
				}
			}
		}
	}
	old := find(items, "old")
	if old == nil || old.Title != "/Users/me/work/api" || old.Group != "-Users-me-work-api" {
		t.Fatalf("the project name comes from the transcript's cwd: %+v", old)
	}
	// The session's own folders travel with it.
	with := strings.Join(companions(*old), "\n")
	for _, want := range []string{"projects/-Users-me-work-api/old", "file-history/old"} {
		if !strings.Contains(with, want) {
			t.Fatalf("companions miss %s: %s", want, with)
		}
	}
	if find(items, "shell-snapshots/old.sh") == nil {
		t.Fatal("scratch folders are listed as leftovers")
	}
	if got := (&Claude{env: env(home)}).Summary(items); got != "4 sessions in 2 projects · 3 older than 30 days" {
		t.Fatalf("summary: %q", got)
	}
}

func ids(items []recipe.Item) string {
	var out []string
	for _, it := range items {
		out = append(out, it.ID)
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

func TestSelectHonoursWindowPinsAndLocks(t *testing.T) {
	home := claudeHome(t)
	items := inspect(t, &Claude{}, home)
	now := time.Now()

	if got := ids(Select(items, 30, nil, nil, now)); got != "mid old only shell-snapshots/old.sh" {
		t.Fatalf("30 days: %s", got)
	}
	if got := ids(Select(items, 75, nil, nil, now)); got != "old shell-snapshots/old.sh" {
		t.Fatalf("75 days: %s", got)
	}
	if got := ids(Select(items, 180, nil, nil, now)); got != "" {
		t.Fatalf("180 days: nothing is that old, got %s", got)
	}
	pinned := map[string]bool{"-Users-me-work-api": true}
	if got := ids(Select(items, 30, pinned, nil, now)); got != "only shell-snapshots/old.sh" {
		t.Fatalf("a pinned project is kept whole: %s", got)
	}
	excluded := map[string]bool{"-Users-me-thesis": true}
	if got := ids(Select(items, 30, nil, excluded, now)); got != "mid old shell-snapshots/old.sh" {
		t.Fatalf("an unchecked project is skipped: %s", got)
	}
	find(items, "old").Locked = "open now"
	if got := ids(Select(items, 30, nil, nil, now)); got != "mid only shell-snapshots/old.sh" {
		t.Fatalf("an open session is never selected: %s", got)
	}
}

func TestClaudePlan(t *testing.T) {
	home := claudeHome(t)
	c := &Claude{}
	items := inspect(t, c, home)
	sel := Select(items, 30, nil, nil, time.Now())

	plan, err := c.Plan(sel, recipe.Options{KeepDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 3 {
		t.Fatalf("one step per project plus one per scratch folder: %d steps", len(plan.Steps))
	}
	api := plan.Steps[0]
	if api.Title != "Remove 2 sessions from /Users/me/work/api" || !api.Reversible || !api.Remove.ToTrash {
		t.Fatalf("sessions go to the Trash: %+v", api)
	}
	if api.Remove.Root != filepath.Join(home, ".claude") {
		t.Fatalf("root: %s", api.Remove.Root)
	}
	last := plan.Steps[2]
	if last.Title != "Clean shell-snapshots/" || last.Tag() != "SAFE" || last.Remove.ToTrash {
		t.Fatalf("leftovers are deleted outright: %+v", last)
	}
	if len(last.Remove.Paths) != 1 || !strings.HasSuffix(last.Remove.Paths[0], "old.sh") {
		t.Fatalf("only old leftovers: %v", last.Remove.Paths)
	}
	if plan.ToTrash <= 0 || plan.Reclaim <= 0 {
		t.Fatalf("totals: trash %d reclaim %d", plan.ToTrash, plan.Reclaim)
	}

	forever, _ := c.Plan(sel, recipe.Options{KeepDays: 30, Permanent: true})
	if forever.Steps[0].Tag() != "IRREVERSIBLE" || forever.Steps[0].Remove.ToTrash {
		t.Fatal("--permanent deletes for good and says so")
	}
	if _, err := c.Plan(nil, recipe.Options{}); err == nil {
		t.Fatal("an empty selection is an error, not an empty plan")
	}
}

func TestManyProjectsFoldIntoOneStep(t *testing.T) {
	var sel []recipe.Item
	for i := 0; i < 25; i++ {
		sel = append(sel, recipe.Item{
			ID: string(rune('a' + i)), Group: string(rune('a' + i)), Title: "/p/" + string(rune('a'+i)),
			Path: "/h/.claude/x", Bytes: int64(100 - i),
		})
	}
	plan, err := buildPlan(spec{id: "claude", name: "Claude Code", dir: "/h/.claude"}, sel, recipe.Options{}, "/h")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != maxProjectSteps {
		t.Fatalf("got %d steps, want %d", len(plan.Steps), maxProjectSteps)
	}
	tail := plan.Steps[maxProjectSteps-1]
	if tail.Title != "Remove 16 sessions from 16 other projects" || len(tail.Remove.Paths) != 16 {
		t.Fatalf("tail: %q with %d paths", tail.Title, len(tail.Remove.Paths))
	}
}

func TestCodex(t *testing.T) {
	home := t.TempDir()
	base := filepath.Join(home, ".codex")
	meta := func(cwd string) string {
		return `{"type":"session_meta","payload":{"base_instructions":"say \"cwd\":\"/fake\" never","cwd":"` + cwd + `"}}` + "\n"
	}
	put(t, filepath.Join(base, "sessions/2026/07/01/rollout-a.jsonl"), meta("/Users/me/work/api"), 140)
	put(t, filepath.Join(base, "sessions/2026/11/20/rollout-b.jsonl"), meta("/Users/me/work/api"), 5)
	put(t, filepath.Join(base, "sessions/2026/09/15/rollout-c.jsonl"), meta("/Users/me/other"), 70)
	put(t, filepath.Join(base, "config.toml"), "model = 1", 200)
	put(t, filepath.Join(base, "auth.json"), "{}", 200)
	put(t, filepath.Join(base, "state_5.sqlite"), "db", 200)
	put(t, filepath.Join(base, "log/codex.log"), "log", 100)

	c := &Codex{}
	items := inspect(t, c, home)
	for _, it := range items {
		for _, never := range []string{"config.toml", "auth.json", ".sqlite"} {
			if strings.Contains(it.Path, never) {
				t.Fatalf("%s must never be offered", it.Path)
			}
		}
	}
	a := find(items, "rollout-a")
	if a == nil || a.Group != "/Users/me/work/api" {
		t.Fatalf("sessions are grouped by the real cwd, not text inside the instructions: %+v", a)
	}
	sel := Select(items, 30, nil, nil, time.Now())
	if got := ids(sel); got != "log/codex.log rollout-a rollout-c" {
		t.Fatalf("selection: %s", got)
	}
	plan, err := c.Plan(sel, recipe.Options{KeepDays: 30})
	if err != nil || len(plan.Steps) != 3 || plan.Steps[2].Title != "Clean log/" {
		t.Fatalf("plan: %v %+v", err, plan.Steps)
	}
}

func TestNotDetectedWithoutFolders(t *testing.T) {
	home := t.TempDir()
	if (&Claude{}).Detect(context.Background(), env(home)) || (&Codex{}).Detect(context.Background(), env(home)) {
		t.Fatal("nothing to detect in an empty home")
	}
}
