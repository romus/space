//go:build darwin

package screens

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/romus/space/internal/recipes/git"
	"github.com/romus/space/internal/ui/theme"
	"github.com/romus/space/pkg/recipe"
)

// fakeGit answers the questions the Git recipe asks, for three made-up
// repositories: web with branches of every kind, legacy with uncommitted
// changes, and api with nothing to remove.
type fakeGit struct{ work string }

func (fakeGit) LookPath(name string) (string, error) {
	if name == "git" {
		return "/opt/homebrew/bin/git", nil
	}
	return "", errors.New("not installed")
}

// When the made-up branches were last committed to, as unix time: the
// screens' clock stands at 2026-11-22 12:00 UTC.
const (
	hoursAgo  = 1795348800 - 2*3600
	monthsAgo = 1795348800 - 95*86400
	yearAgo   = 1795348800 - 400*86400
)

func (f fakeGit) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	if name == "env" { // the question to origin
		return []byte("a\trefs/heads/main\nb\trefs/heads/develop\nc\trefs/heads/release/1\n"), nil
	}
	repo, sub, rest := "", "", []string(nil)
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-C" || args[i] == "-c":
			if args[i] == "-C" {
				repo = filepath.Base(args[i+1])
			}
			i++
		case strings.HasPrefix(args[i], "--no-"):
		default:
			sub, rest = args[i], args[i+1:]
			i = len(args)
		}
	}
	has := func(flag string) bool { return strings.Contains(strings.Join(rest, " "), flag) }
	ref := func(name string, when int, upstream, track, head, wt, sym string) string {
		return fmt.Sprintf("%s\tabc\t%d\t%s\t%s\t%s\t%s\t%s\n", name, when, upstream, track, head, wt, sym)
	}
	out := ""
	switch {
	case sub == "config":
		out = "git@github.com:me/" + repo + ".git"
	case sub == "status" && repo == "legacy":
		out = " M billing.go"
	case sub == "worktree" && repo == "web":
		out = "worktree " + f.work + "/web\nbranch refs/heads/main\n\nworktree " + f.work + "/web-hotfix\nbranch refs/heads/hotfix\n"
	case sub == "rev-list" && has("--count"):
		out = "2"
	case sub == "rev-list":
		out = "780000000"
	case sub == "count-objects":
		out = "size-garbage: 0"
	case sub == "for-each-ref" && has("--merged"):
		out = "refs/heads/main\nrefs/heads/fix/login\nrefs/heads/hotfix\n"
		for _, n := range []string{"a", "b", "c", "d"} {
			out += "refs/heads/dependabot/npm/" + n + "\n"
		}
	case sub == "for-each-ref" && repo == "web":
		out = ref("refs/heads/main", hoursAgo, "refs/remotes/origin/main", "", "*", f.work+"/web", "") +
			ref("refs/heads/develop", hoursAgo, "refs/remotes/origin/develop", "", " ", "", "") +
			ref("refs/heads/fix/login", monthsAgo, "", "", " ", "", "") +
			ref("refs/heads/exp/webgl", monthsAgo, "refs/remotes/origin/exp/webgl", "[gone]", " ", "", "") +
			ref("refs/heads/release/1", yearAgo, "refs/remotes/origin/release/1", "", " ", "", "") +
			ref("refs/heads/wip/pricing", monthsAgo, "", "", " ", "", "") +
			ref("refs/heads/hotfix", hoursAgo, "", "", " ", f.work+"/web-hotfix", "") +
			ref("refs/remotes/origin/HEAD", hoursAgo, "", "", " ", "", "refs/remotes/origin/main") +
			ref("refs/remotes/origin/main", hoursAgo, "", "", " ", "", "") +
			ref("refs/remotes/origin/develop", hoursAgo, "", "", " ", "", "") +
			ref("refs/remotes/origin/release/1", yearAgo, "", "", " ", "", "") +
			ref("refs/remotes/origin/old-a", yearAgo, "", "", " ", "", "") +
			ref("refs/remotes/origin/old-b", yearAgo, "", "", " ", "", "")
		for i, n := range []string{"a", "b", "c", "d"} {
			out += ref("refs/heads/dependabot/npm/"+n, monthsAgo-i*40*86400, "", "", " ", "", "")
		}
	case sub == "for-each-ref":
		out = ref("refs/heads/main", hoursAgo, "refs/remotes/origin/main", "", "*", "", "") +
			ref("refs/heads/old", yearAgo, "", "", " ", "", "") +
			ref("refs/remotes/origin/main", hoursAgo, "", "", " ", "", "")
	}
	return []byte(out), nil
}

// gitScreen opens the Git recipe on a made-up ~/work, or inside one of its
// repositories.
func gitScreen(t *testing.T, inside string) (*Git, *Ctx) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(home, "work")
	for name, kb := range map[string]int{"web": 2400, "legacy": 1100, "api": 96} {
		dir := filepath.Join(work, name, ".git")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "pack"), make([]byte, kb*1000), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	os.MkdirAll(filepath.Join(work, "web-hotfix"), 0o755)

	c := testCtx(theme.DarkTheme())
	c.Paths.Home, c.Paths.StateDir = home, filepath.Join(home, ".local/state/space")
	c.Root = filepath.Join(work, inside)
	c.Env.Home, c.Env.Root, c.Env.StateDir, c.Env.Run = home, c.Root, c.Paths.StateDir, fakeGit{work}
	r := &git.Recipe{}
	if !r.Detect(context.Background(), c.Env) {
		t.Fatal("not detected")
	}
	items, err := r.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	g := NewGit(c, &RecipeState{R: r, Items: items, Loaded: true})
	settle(g, g.Start())
	return g, c
}

// settle runs commands and feeds what they return back to the screen,
// the way the Bubble Tea runtime would, until nothing is left.
func settle(s Screen, cmd tea.Cmd) {
	for queue := []tea.Cmd{cmd}; len(queue) > 0; queue = queue[1:] {
		if queue[0] == nil {
			continue
		}
		switch m := queue[0]().(type) {
		case nil:
		case tea.BatchMsg:
			queue = append(queue, m...)
		default:
			_, next := s.Update(m)
			queue = append(queue, next)
		}
	}
}

// hit presses keys one after another and lets the screen settle.
func hit(s Screen, keys ...string) {
	for _, k := range keys {
		_, cmd := s.Update(pressMsg(k))
		settle(s, cmd)
	}
}

func TestGitRepositories(t *testing.T) {
	g, c := gitScreen(t, "")
	golden(t, "git_repos.golden", strings.ReplaceAll(plain(t, g, c), c.Home(), "~"))

	// A skipped repository cannot be included, and says why.
	hit(g, "j", "j", " ")
	wantAll(t, text(t, g, c), "legacy: uncommitted changes — it is skipped")
	// An included one can be left out.
	hit(g, "g", " ")
	wantAll(t, text(t, g, c), "[ ] web", "1 repo · 0 branches")
	hit(g, "g", " ")

	// The switches.
	hit(g, "c")
	s := text(t, g, c)
	wantAll(t, s, "c [ ] compact .git", "≈ 0 B")
	hit(g, "c", "p")
	wantAll(t, text(t, g, c), "p [ ] prune stale origin refs")
	if strings.Contains(text(t, g, c), "stale origin refs ·") {
		t.Fatal("without pruning the note does not promise it")
	}
	hit(g, "p", "b")
	wantAll(t, text(t, g, c), "b [ ] back up what is not merged")
	_, cmd := g.Update(pressMsg("enter"))
	for _, s := range cmd().(PushMsg).Screen.(*Plan).spec.Plan.Steps {
		if strings.HasPrefix(s.Title, "Back up") {
			t.Fatal("with backups off the plan saves nothing")
		}
	}
}

func TestGitRule(t *testing.T) {
	g, c := gitScreen(t, "")
	hit(g, "e")
	if !g.Typing() {
		t.Fatal("e starts editing the rule")
	}
	wantAll(t, text(t, g, c), "main master develop▍", "enter save the rule")
	keys := []string{}
	for _, r := range " release/*" {
		keys = append(keys, string(r))
	}
	hit(g, append(keys, "enter")...)
	if g.Typing() || strings.Join(c.State.GitKeep, " ") != "main master develop release/*" {
		t.Fatalf("the rule is saved: %q", c.State.GitKeep)
	}
	wantAll(t, text(t, g, c), "keep    main master develop release/*", "6 of 11")
	hit(g, "l")
	wantAll(t, text(t, g, c), "release/1", "kept · rule")
	// A branch the rule keeps cannot be marked.
	g.rcur = g.rowOf("b/release/1")
	hit(g, " ")
	wantAll(t, text(t, g, c), "release/1 is kept by the rule")

	// An empty rule is the rule out of the box again.
	hit(g, "esc", "e")
	for range "main master develop release/*" {
		hit(g, "backspace")
	}
	hit(g, "enter")
	if len(c.State.GitKeep) != 0 || strings.Join(g.keep, " ") != "main master develop" {
		t.Fatalf("an empty rule: state=%q keep=%q", c.State.GitKeep, g.keep)
	}
}

func TestGitOneRepository(t *testing.T) {
	g, c := gitScreen(t, "")
	hit(g, "l")
	golden(t, "git_repo.golden", strings.ReplaceAll(plain(t, g, c), c.Home(), "~"))

	// Opening a fold shows its branches; closing folds them again.
	g.rcur = g.rowOf("b/dependabot/npm/a")
	hit(g, "l")
	wantAll(t, text(t, g, c), "dependabot/npm/a", "dependabot/npm/d")
	hit(g, "h")
	wantAll(t, text(t, g, c), "dependabot/npm/*  × 4")
	// Marking a fold marks all of it.
	hit(g, " ")
	wantAll(t, text(t, g, c), "[ ] dependabot/npm/*  × 4", "3 branches")

	// What exists only here is never picked; the user can pick it.
	g.rcur = g.rowOf("b/wip/pricing")
	wantAll(t, text(t, g, c), "[ ] wip/pricing", "only here · 2 commits")
	hit(g, " ")
	wantAll(t, text(t, g, c), "[✓] wip/pricing", "4 branches")
	// Merged only: the careful choice.
	hit(g, "m")
	wantAll(t, text(t, g, c), "[ ] wip/pricing", "[ ] exp/webgl", "[✓] fix/login", "5 branches")
	// What is locked says why.
	g.rcur = g.rowOf("b/main")
	hit(g, " ")
	wantAll(t, text(t, g, c), "main stays: the main branch")

	// Remote-tracking: what origin no longer has is stale and picked.
	g.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	wantAll(t, text(t, g, c), "Remote-tracking 2 stale", "[✓] origin/old-a", "gone on origin",
		" ○  origin/develop", "still on origin", " ○  origin/main", "kept · default")
	// Worktrees: a clean one can be marked, and nothing is picked for you.
	g.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	wantAll(t, text(t, g, c), "Worktrees 1", "[ ] ~/work/web-hotfix", "clean · on hotfix")
	hit(g, " ")
	wantAll(t, text(t, g, c), "[✓] ~/work/web-hotfix")

	// The plan is for this repository only.
	_, cmd := g.Update(pressMsg("enter"))
	msg, ok := cmd().(PushMsg)
	if !ok {
		t.Fatal("enter opens the plan")
	}
	s := text(t, msg.Screen, c)
	wantAll(t, s, "Recipes › Git · ~/work › Plan", "7 steps in web", "Delete branches  5 in total",
		"Prune stale refs", "Remove worktrees", "$ trash ~/work/web-hotfix",
		"skipped    legacy — uncommitted changes", "run in 1 repo · ≈ 780 MB")
	if p := msg.Screen.(*Plan); !p.irreversible() || len(p.spec.Plan.Steps) != 8 || p.spec.Rebuild == nil {
		t.Fatalf("the plan forgets and compacts, and can be rebuilt: %d steps", len(p.spec.Plan.Steps))
	}

	hit(g, "esc")
	if g.open != -1 {
		t.Fatal("esc goes back to every repository")
	}
	// From the list the plan covers every included repository.
	_, cmd = g.Update(pressMsg("enter"))
	wantAll(t, text(t, cmd().(PushMsg).Screen, c), "Up to 7 steps in each of 2 repos", "4 at a time", "in         web · api", "  2  Back up branches")
}

func TestGitInsideARepository(t *testing.T) {
	g, c := gitScreen(t, "web/src/..")
	if !g.single || g.open != 0 {
		t.Fatal("inside a repository the screen opens on its branches")
	}
	wantAll(t, text(t, g, c), "Recipes › ◆ Git › web", "origin github.com/me/web", "esc back")
	_, cmd := g.Update(pressMsg("esc"))
	if _, ok := cmd().(PopMsg); !ok {
		t.Fatal("there is no list to go back to: esc leaves the recipe")
	}
}

func TestGitWhenOriginDoesNotAnswer(t *testing.T) {
	g, c := gitScreen(t, "")
	web := g.repos[0].Path
	g.rs.Items = git.ApplyRemote(g.rs.Items, web, nil, errors.New("no route to host"))
	wantAll(t, text(t, g, c), "origin did not answer")
	hit(g, "l")
	g.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	wantAll(t, text(t, g, c), "origin did not answer: nothing here can be told stale", " ○  origin/old-a")
	if n := g.taken(web, git.KindRemote); n != 0 {
		t.Fatalf("nothing remote is taken on a guess, got %d", n)
	}
}

func TestGitScreenSurvivesAnyWindow(t *testing.T) {
	g, c := gitScreen(t, "")
	for _, size := range [][2]int{{80, 20}, {96, 24}, {160, 50}, {40, 10}} {
		c.W, c.H = size[0], size[1]
		g.open = -1
		text(t, g, c)
		hit(g, "e")
		text(t, g, c)
		hit(g, "esc", "l")
		for range 3 {
			for range 14 {
				hit(g, "j")
				text(t, g, c)
			}
			g.Update(tea.KeyPressMsg{Code: tea.KeyTab})
		}
	}
	var _ recipe.Recipe = g.rec
}

// A repository that is left alone can be opened and looked at. It says why
// it is left alone, nothing in it can be marked, and there is no plan.
func TestGitRepositoryLeftAlone(t *testing.T) {
	g, c := gitScreen(t, "")
	hit(g, "j", "j", "l")
	s := text(t, g, c)
	wantAll(t, s, "◆ Git › legacy", "Left alone: uncommitted changes.", "[ ] old", "left alone · uncommitted changes")
	if strings.Contains(s, "[✓]") {
		t.Fatal("nothing is shown as taken in a repository that is left alone")
	}
	hit(g, "j", " ")
	wantAll(t, text(t, g, c), "legacy is left alone: uncommitted changes. Nothing in it can be marked.")
	if len(g.marks) != 0 {
		t.Fatal("nothing can be marked there")
	}
	if _, cmd := g.Update(pressMsg("enter")); cmd != nil {
		t.Fatal("there is no plan for it")
	}
	wantAll(t, text(t, g, c), "No plan: legacy is left alone — uncommitted changes")

	// A repository the user left out says so, and how to bring it back.
	hit(g, "esc", "g", " ", "g", "l", "enter")
	wantAll(t, text(t, g, c), "No plan: web is left out — include it with ␣ on the list")
	// And with nothing included, the list says that.
	hit(g, "esc", "j", " ", "enter")
	wantAll(t, text(t, g, c), "No plan: every repository here is left alone or left out")
}
