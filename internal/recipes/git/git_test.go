//go:build darwin

package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/romus/space/pkg/recipe"
)

// What git 2.54 printed for a small repository, with refFormat.
const refsOut = "refs/heads/gone\t4cac1c2\t1795729903\trefs/remotes/origin/gone\t[gone]\t \t\t\n" +
	"refs/heads/main\tb144066\t1795729903\trefs/remotes/origin/main\t[ahead 1]\t*\t/w/repo\t\n" +
	"refs/heads/merged\tb144066\t1795729903\t\t\t \t\t\n" +
	"refs/heads/only\tac327dc\t1795729903\t\t\t \t\t\n" +
	"refs/heads/ahead\tac327dd\t1795729903\trefs/remotes/origin/ahead\t[ahead 2, behind 1]\t \t\t\n" +
	"refs/heads/pushed\t6a4ddb7\t1795729903\trefs/remotes/origin/pushed\t\t \t\t\n" +
	"refs/heads/wtbranch\tb144066\t1795729903\t\t\t \t/w/wt\t\n" +
	"refs/remotes/origin/HEAD\t11eb954\t1795729903\t\t\t \t\trefs/remotes/origin/main\n" +
	"refs/remotes/origin/main\t11eb954\t1795729903\t\t\t \t\t\n"

func TestParseRefsAndWhy(t *testing.T) {
	refs := parseRefs(refsOut)
	if len(refs) != 9 {
		t.Fatalf("%d refs, want 9", len(refs))
	}
	if def := defaultBranch(refs); def != "main" {
		t.Fatalf("default branch: %q", def)
	}
	merged := map[string]bool{"refs/heads/main": true, "refs/heads/merged": true, "refs/heads/wtbranch": true}
	want := map[string]string{
		"gone": WhyGone, "main": WhyDefault, "merged": WhyMerged, "only": WhyOnly + " 3",
		"ahead": WhyOnly + " 2", "pushed": WhyPushed, "wtbranch": WhyWorktree,
	}
	for _, r := range refs {
		if !r.local() {
			continue
		}
		reason, n := why(r, "main", merged, 3)
		got := reason
		if n > 0 {
			got += " " + string(rune('0'+n))
		}
		if got != want[r.short()] {
			t.Errorf("%s: %q, want %q", r.short(), got, want[r.short()])
		}
	}
	if !refs[1].head || refs[1].when.Unix() != 1795729903 || refs[6].worktree != "/w/wt" {
		t.Fatalf("fields: %+v", refs[1])
	}
}

func TestDefaultBranchWithoutOrigin(t *testing.T) {
	cases := map[string]string{
		"refs/heads/master\ta\t1\t\t\t*\t\t\nrefs/heads/x\ta\t1\t\t\t \t\t\n": "master",
		"refs/heads/trunk\ta\t1\t\t\t*\t\t\nrefs/heads/x\ta\t1\t\t\t \t\t\n":  "trunk",
		"refs/heads/main\ta\t1\t\t\t \t\t\nrefs/heads/x\ta\t1\t\t\t*\t\t\n":   "main",
	}
	for out, want := range cases {
		if got := defaultBranch(parseRefs(out)); got != want {
			t.Errorf("default of %q: %q, want %q", out, got, want)
		}
	}
}

func TestParseWorktrees(t *testing.T) {
	out := "worktree /w/repo\nHEAD b144\nbranch refs/heads/main\n\n" +
		"worktree /w/wt\nHEAD b144\nbranch refs/heads/hotfix\n\n" +
		"worktree /w/gone\nHEAD b144\ndetached\nprunable gitdir file points to non-existent location\n\n" +
		"worktree /w/kept\nHEAD b144\nbranch refs/heads/x\nlocked reason\n"
	got := parseWorktrees(out)
	if len(got) != 3 {
		t.Fatalf("the repository's own folder is not a linked worktree: %+v", got)
	}
	if got[0].path != "/w/wt" || got[0].branch != "hotfix" || !got[1].prunable || got[1].branch != "" || !got[2].locked {
		t.Fatalf("worktrees: %+v", got)
	}
	if parseWorktrees("") != nil {
		t.Fatal("no output, no worktrees")
	}
}

func TestKeepRule(t *testing.T) {
	keep := ParseKeep("main, master  release/* v?")
	if strings.Join(keep, "|") != "main|master|release/*|v?" {
		t.Fatalf("parsed: %q", keep)
	}
	for name, want := range map[string]bool{
		"main": true, "release/2025.1": true, "release/a/b": false, "v2": true, "v10": false, "feature/main": false,
	} {
		if Kept(name, keep) != want {
			t.Errorf("Kept(%q) = %v, want %v", name, !want, want)
		}
	}
}

func item(repo, kind, title, why string) recipe.Item {
	id := map[string]string{KindRepo: RepoID, KindBranch: "b/" + title, KindRemote: "r/" + title, KindWorktree: "w/" + title}[kind]
	return recipe.Item{ID: id, Group: repo, Title: title, Path: title, Meta: map[string]string{MetaKind: kind, MetaWhy: why}}
}

func locked(it recipe.Item, why string) recipe.Item {
	it.Locked = why
	return it
}

func ids(items []recipe.Item) string {
	var out []string
	for _, it := range items {
		out = append(out, filepath.Base(it.Group)+":"+it.ID)
	}
	return strings.Join(out, " ")
}

func TestSelect(t *testing.T) {
	only := item("/w/web", KindBranch, "wip", WhyOnly)
	items := []recipe.Item{
		item("/w/web", KindRepo, "web", ""),
		locked(item("/w/web", KindBranch, "main", WhyDefault), "the main branch"),
		item("/w/web", KindBranch, "develop", WhyPushed),
		item("/w/web", KindBranch, "fix", WhyMerged),
		item("/w/web", KindBranch, "exp", WhyGone),
		only,
		item("/w/web", KindRemote, "origin/old", WhyStale),
		locked(item("/w/web", KindRemote, "origin/live", WhyLive), "origin still has it"),
		item("/w/web", KindWorktree, "/w/web-wt", WhyClean),
		item("/w/web", KindWorktree, "/w/web-gone", WhyMissing),
		locked(item("/w/old", KindRepo, "old", ""), "uncommitted changes"),
		item("/w/old", KindBranch, "fix", WhyMerged),
		item("/w/api", KindRepo, "api", ""),
		item("/w/api", KindBranch, "fix", WhyMerged),
	}
	got := ids(Select(items, DefaultKeep, nil))
	want := "web:repo web:b/fix web:b/exp web:r/origin/old web:w//w/web-gone api:repo api:b/fix"
	if got != want {
		t.Fatalf("default selection:\n got %s\nwant %s", got, want)
	}

	// The user's marks: take the branch that exists only here and the clean
	// worktree, leave one branch, and leave api out entirely.
	marks := map[string]bool{
		MarkKey(only):      true,
		MarkKey(items[8]):  true,
		MarkKey(items[3]):  false,
		MarkKey(items[12]): false,
		MarkKey(items[1]):  true, // locked: a mark cannot take it
		MarkKey(items[2]):  true, // kept by the rule: neither
	}
	got = ids(Select(items, DefaultKeep, marks))
	want = "web:repo web:b/exp web:b/wip web:r/origin/old web:w//w/web-wt web:w//w/web-gone"
	if got != want {
		t.Fatalf("with marks:\n got %s\nwant %s", got, want)
	}

	// A wider rule keeps more; an empty one keeps only what is locked.
	if got := ids(Select(items[:6], []string{"fix", "exp"}, nil)); got != "web:repo web:b/develop" {
		t.Fatalf("with another rule: %s", got)
	}
}

func TestApplyRemote(t *testing.T) {
	pushed := item("/w/web", KindBranch, "feat", WhyPushed)
	pushed.Meta[MetaUpstream] = "origin/feat"
	items := []recipe.Item{
		item("/w/web", KindRepo, "web", ""),
		pushed,
		locked(item("/w/web", KindRemote, "origin/feat", WhyUnchecked), "origin was not asked yet"),
		locked(item("/w/web", KindRemote, "origin/live", WhyUnchecked), "origin was not asked yet"),
		locked(item("/w/web", KindRemote, "origin/main", WhyDefault), "the main branch on origin"),
		locked(item("/w/api", KindRemote, "origin/feat", WhyUnchecked), "origin was not asked yet"),
	}
	got := ApplyRemote(items, "/w/web", map[string]bool{"live": true, "main": true}, nil)
	if got[0].Meta[MetaRemote] != RemoteChecked || got[1].Meta[MetaWhy] != WhyGone {
		t.Fatalf("repo and branch: %+v %+v", got[0].Meta, got[1].Meta)
	}
	if got[2].Meta[MetaWhy] != WhyStale || got[2].Locked != "" || !got[2].Default {
		t.Fatalf("a branch origin no longer has is stale and can go: %+v", got[2])
	}
	if got[3].Meta[MetaWhy] != WhyLive || got[3].Locked == "" || got[4].Meta[MetaWhy] != WhyDefault {
		t.Fatalf("what origin has stays: %+v %+v", got[3], got[4])
	}
	if got[5].Meta[MetaWhy] != WhyUnchecked || items[2].Meta[MetaWhy] != WhyUnchecked {
		t.Fatal("other repositories and the items passed in are not changed")
	}

	down := ApplyRemote(items, "/w/web", nil, errors.New("no route"))
	if down[0].Meta[MetaRemote] != RemoteUnreachable || down[2].Locked != "origin did not answer" || down[1].Meta[MetaWhy] != WhyPushed {
		t.Fatalf("when origin does not answer nothing remote is offered: %+v", down[:3])
	}
}

func TestShortURLAndRepoName(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.com:me/web.git":          "github.com/me/web",
		"https://github.com/me/web.git":      "github.com/me/web",
		"https://me@gitlab.example.com/g/w":  "gitlab.example.com/g/w",
		"ssh://git@host.example:2222/me/web": "host.example:2222/me/web",
		"../origin.git":                      "../origin",
	} {
		if got := shortURL(in); got != want {
			t.Errorf("shortURL(%q) = %q, want %q", in, got, want)
		}
	}
	for path, want := range map[string]string{"/w/web": "web", "/w/team/api": "team/api", "/w": "w", "/other/x": "x", "/w/a\nb": "a b"} {
		if got := repoName("/w", path); got != want {
			t.Errorf("repoName(%q) = %q, want %q", path, got, want)
		}
	}
}

// planner is a recipe that has "read" two repositories.
func planner(t *testing.T) (*Recipe, []recipe.Item) {
	t.Helper()
	home := t.TempDir()
	r := &Recipe{env: recipe.Env{
		Home: home, Root: "/w", StateDir: filepath.Join(home, ".local/state/space"),
		Run: noLFS{}, Now: func() time.Time { return time.Date(2026, 11, 27, 15, 4, 5, 0, time.UTC) },
	}}
	r.order = []string{"/w/web", "/w/old", "/w/api"}
	r.repos = map[string]Repo{
		"/w/web": {Path: "/w/web", Name: "web", Base: "refs/heads/main", GitBytes: 2_400_000_000},
		"/w/old": {Path: "/w/old", Name: "old", Skip: "uncommitted changes"},
		"/w/api": {Path: "/w/api", Name: "api", Base: "refs/heads/main", GitBytes: 100_000_000},
	}
	web := item("/w/web", KindRepo, "web", "")
	web.Bytes = 780_000_000
	fix := item("/w/web", KindBranch, "fix/login", WhyMerged)
	fix.Meta[MetaMerged] = "1"
	wt := item("/w/web", KindWorktree, "/w/web-hotfix", WhyClean)
	wt.Bytes = 5_000_000
	api := item("/w/api", KindRepo, "api", "")
	api.Bytes = 12_000_000
	return r, []recipe.Item{
		web, fix,
		item("/w/web", KindBranch, "exp/webgl", WhyGone),
		item("/w/web", KindRemote, "origin/old", WhyStale),
		wt,
		api,
	}
}

type noLFS struct{}

func (noLFS) Output(context.Context, string, ...string) ([]byte, error) {
	return nil, errors.New("not run")
}
func (noLFS) LookPath(string) (string, error) { return "", errors.New("not installed") }

func TestPlanSteps(t *testing.T) {
	r, sel := planner(t)
	plan, err := r.Plan(sel, recipe.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range plan.Steps {
		cmd := strings.Join(s.Cmd, " ")
		if s.Remove != nil {
			cmd = "trash " + strings.Join(s.Remove.Paths, " ") + " (in " + s.Remove.Root + ")"
		}
		got = append(got, s.Group+" | "+s.Tag()+" | "+strings.ReplaceAll(cmd, r.env.Home, "~"))
	}
	const g = "git -C /w/web -c core.hooksPath=/dev/null -c core.fsmonitor=false "
	want := []string{
		" | SAFE | mkdir -p ~/.local/state/space/git",
		"web | SAFE | " + g + "bundle create ~/.local/state/space/git/web-" + mark("/w/web") + "-20261127-150405.bundle refs/heads/exp/webgl refs/remotes/origin/old --not refs/heads/main",
		"web | REVERSIBLE | " + g + "branch -D fix/login exp/webgl",
		"web | REVERSIBLE | " + g + "branch -d -r origin/old",
		"web | REVERSIBLE | trash /w/web-hotfix (in /w)",
		"web | SAFE | " + g + "worktree prune",
		"web | IRREVERSIBLE | " + g + "-c gc.reflogExpireUnreachable=now reflog expire --all",
		"web | SAFE | " + g + "gc --prune=now",
		"api | IRREVERSIBLE | git -C /w/api -c core.hooksPath=/dev/null -c core.fsmonitor=false -c gc.reflogExpireUnreachable=now reflog expire --all",
		"api | SAFE | git -C /w/api -c core.hooksPath=/dev/null -c core.fsmonitor=false gc --prune=now",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("steps:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if plan.Parallel != 4 || plan.GroupNoun != "repo" || plan.Reclaim != 792_000_000 || plan.ToTrash != 5_000_000 {
		t.Fatalf("plan: parallel=%d noun=%q reclaim=%d trash=%d", plan.Parallel, plan.GroupNoun, plan.Reclaim, plan.ToTrash)
	}
	if plan.Steps[2].Kind != "Delete branches\t2 in total" || plan.Steps[1].Title != "Back up 2 branches" {
		t.Fatalf("titles: %q %q", plan.Steps[2].Kind, plan.Steps[1].Title)
	}
	if strings.Join(plan.Notes, "|") != "skipped\told — uncommitted changes|"+
		"backups\tkept 30 days in ~/.local/state/space/git — the space of unmerged branches comes back then|"+
		"untouched\tbranches on origin · stashes · tags · working files" {
		t.Fatalf("notes: %q", plan.Notes)
	}
}

func mark(path string) string {
	r := &Recipe{}
	name := filepath.Base(r.bundlePath(Repo{Path: path}, time.Time{}))
	return strings.Split(strings.TrimPrefix(name, filepath.Base(path)+"-"), "-")[0]
}

func TestPlanToggles(t *testing.T) {
	r, sel := planner(t)
	plan, err := r.Plan(sel, recipe.Options{Toggles: map[string]bool{ToggleCompact: false, TogglePrune: false}})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range plan.Steps {
		joined := strings.Join(s.Cmd, " ")
		if s.Irreversible() || strings.Contains(joined, " gc ") || strings.Contains(joined, "-d -r") || s.Group == "api" {
			t.Fatalf("without compacting and pruning this step has no place: %s", joined)
		}
	}
	if plan.Reclaim != 0 {
		t.Fatal("nothing comes back without compacting")
	}

	// Without a backup nothing is saved, and deleting what is not merged
	// is no longer something that can be undone.
	plan, err = r.Plan(sel, recipe.Options{Toggles: map[string]bool{ToggleBackup: false}})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range plan.Steps {
		if strings.Contains(strings.Join(s.Cmd, " "), "bundle") || s.Title == "Create the backup folder" {
			t.Fatalf("no backup was asked for: %s", s.Title)
		}
		if strings.HasPrefix(s.Title, "Delete") && (s.Reversible || !strings.Contains(s.Why, "Not backed up")) {
			t.Fatalf("deleting an unmerged branch without a backup cannot be undone: %+v", s)
		}
	}

	// Nothing marked and nothing to compact: there is no plan to show.
	if _, err := r.Plan(sel[5:], recipe.Options{Toggles: map[string]bool{ToggleCompact: false}}); err == nil {
		t.Fatal("an empty plan must be an error")
	}
	if _, err := r.Plan(sel[1:3], recipe.Options{}); err == nil {
		t.Fatal("branches without their repository are not a plan")
	}
}

func TestOldBackupsAreRemoved(t *testing.T) {
	r, sel := planner(t)
	dir := r.BackupDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := r.env.Now()
	for name, age := range map[string]time.Duration{"old.bundle": 31 * 24 * time.Hour, "new.bundle": 29 * 24 * time.Hour, "notes.txt": 90 * 24 * time.Hour} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(path, now.Add(-age), now.Add(-age))
	}
	plan, err := r.Plan(sel, recipe.Options{})
	if err != nil {
		t.Fatal(err)
	}
	s := plan.Steps[1]
	if s.Remove == nil || len(s.Remove.Paths) != 1 || filepath.Base(s.Remove.Paths[0]) != "old.bundle" ||
		!s.Remove.ToTrash || s.Remove.Root != dir || s.Group != "" {
		t.Fatalf("only bundles past 30 days go, to the Trash, once for all: %+v", s)
	}
}
