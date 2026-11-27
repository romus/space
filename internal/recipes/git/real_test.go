//go:build darwin

package git

import (
	"context"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/romus/space/internal/engine"
	"github.com/romus/space/internal/scan"
	"github.com/romus/space/pkg/recipe"
)

// These tests run the real git on repositories built in a temporary
// folder, and the real engine on the plan. They are the proof that the
// commands of the plan do what the plan says, and nothing more.

// sandbox makes a home with a work folder in it and keeps git away from
// the user's own settings.
func sandbox(t *testing.T) (home, work string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPACE_HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, who := range []string{"AUTHOR", "COMMITTER"} {
		t.Setenv("GIT_"+who+"_NAME", "Test")
		t.Setenv("GIT_"+who+"_EMAIL", "test@example.com")
	}
	work = filepath.Join(home, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	return home, work
}

// run runs git in dir and returns what it printed.
func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
}

// commit adds one file with content and commits it.
func commit(t *testing.T, repo, file, content string) {
	t.Helper()
	write(t, filepath.Join(repo, file), []byte(content))
	run(t, repo, "add", file)
	run(t, repo, "commit", "-q", "-m", "add "+file)
}

// bigRepo builds a repository with one branch of every kind:
//
//	main      the main branch, checked out
//	develop   kept by the rule
//	merged    in main
//	pushed    on origin
//	gone      deleted on origin, holds 2 MB that exist nowhere else
//	only      never pushed
//	wt        checked out in the linked worktree ../<name>-wt
//	origin/stale  a remote-tracking branch origin no longer has
//
// plus two stashes, a hook and a file system monitor that leave a mark
// when git runs them.
func bigRepo(t *testing.T, work, name string) string {
	t.Helper()
	origin := filepath.Join(filepath.Dir(work), "origins", name+".git")
	run(t, filepath.Dir(work), "init", "-q", "--bare", "-b", "main", origin)
	repo := filepath.Join(work, name)
	run(t, work, "init", "-q", "-b", "main", repo)
	run(t, repo, "remote", "add", "origin", origin)
	commit(t, repo, "readme", "one\n")
	run(t, repo, "push", "-q", "-u", "origin", "main")
	branch := func(name string, then func()) {
		run(t, repo, "checkout", "-q", "-b", name, "main")
		then()
		run(t, repo, "checkout", "-q", "main")
	}
	branch("develop", func() { commit(t, repo, "dev", "dev\n"); run(t, repo, "push", "-q", "-u", "origin", "develop") })
	branch("merged", func() { commit(t, repo, "feature", "done\n") })
	run(t, repo, "merge", "-q", "--ff-only", "merged")
	branch("pushed", func() { commit(t, repo, "pushed", "p\n"); run(t, repo, "push", "-q", "-u", "origin", "pushed") })
	branch("gone", func() {
		noise := make([]byte, 2<<20)
		rand.New(rand.NewSource(1)).Read(noise) // random: it does not compress away
		write(t, filepath.Join(repo, "big.bin"), noise)
		run(t, repo, "add", "big.bin")
		run(t, repo, "commit", "-q", "-m", "big")
		run(t, repo, "push", "-q", "-u", "origin", "gone")
		run(t, repo, "push", "-q", "origin", "--delete", "gone")
	})
	branch("only", func() { commit(t, repo, "wip", "wip\n") })
	branch("stale", func() { commit(t, repo, "stale", "s\n"); run(t, repo, "push", "-q", "-u", "origin", "stale") })
	run(t, repo, "branch", "-q", "-D", "stale")
	run(t, origin, "branch", "-q", "-D", "stale")

	for _, text := range []string{"first stash\n", "second stash\n"} {
		write(t, filepath.Join(repo, "readme"), []byte(text))
		run(t, repo, "stash", "-q")
	}
	run(t, repo, "worktree", "add", "-q", "-b", "wt", filepath.Join(work, name+"-wt"), "main")

	// Each leaves a line saying who it is and which command started it.
	marker := filepath.Join(filepath.Dir(work), "ran-"+name)
	script := func(who string) []byte {
		return []byte("#!/bin/sh\necho \"" + who + " <- $(ps -o command= -p $PPID)\" >> " + marker + "\n")
	}
	write(t, filepath.Join(repo, ".git/hooks/reference-transaction"), script("hook"))
	write(t, filepath.Join(repo, ".git/fsmonitor.sh"), script("fsmonitor"))
	run(t, repo, "config", "core.fsmonitor", filepath.Join(repo, ".git/fsmonitor.sh"))
	os.Remove(marker) // git ran the hook while the repository was built
	return repo
}

func newRecipe(t *testing.T, home, root string) *Recipe {
	t.Helper()
	r := &Recipe{}
	env := recipe.Env{Home: home, Root: root, StateDir: filepath.Join(home, ".local/state/space"), Run: engine.Reader{}, Now: time.Now}
	if !r.Detect(context.Background(), env) {
		t.Fatal("git is installed and a folder is open: the recipe must be detected")
	}
	return r
}

func branches(t *testing.T, repo string) string {
	return strings.Join(strings.Fields(run(t, repo, "for-each-ref", "--format=%(refname:short)", "refs/heads", "refs/remotes")), " ")
}

func whys(items []recipe.Item, repo string) string {
	var out []string
	for _, it := range items {
		if it.Group == repo && it.Meta[MetaKind] != KindRepo {
			out = append(out, it.Title[strings.LastIndex(it.Title, "/")+1:]+"="+it.Meta[MetaWhy])
		}
	}
	return strings.Join(out, " ")
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func TestRealGitCleanup(t *testing.T) {
	home, work := sandbox(t)
	ctx := context.Background()
	web := bigRepo(t, work, "web")

	// A repository with uncommitted changes, and one with a detached HEAD.
	dirty := filepath.Join(work, "dirty")
	run(t, work, "init", "-q", "-b", "main", dirty)
	commit(t, dirty, "a", "a\n")
	run(t, dirty, "branch", "old")
	write(t, filepath.Join(dirty, "a"), []byte("changed\n"))
	detached := filepath.Join(work, "team", "detached")
	os.MkdirAll(filepath.Dir(detached), 0o755)
	run(t, filepath.Dir(detached), "init", "-q", "-b", "main", detached)
	commit(t, detached, "a", "a\n")
	run(t, detached, "checkout", "-q", "--detach")
	// Not repositories of their own: a clone a tool keeps in a hidden
	// folder, and a folder that only holds a worktree.
	run(t, work, "init", "-q", filepath.Join(work, ".cache", "plugin"))

	r := newRecipe(t, home, work)
	items, err := r.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, repo := range r.Repos() {
		names = append(names, repo.Name+"("+repo.Skip+")")
	}
	if got := strings.Join(names, " "); got != "dirty(uncommitted changes) team/detached(detached HEAD) web()" {
		t.Fatalf("repositories: %s", got)
	}
	want := "develop=pushed gone=gone main=default merged=merged only=only pushed=pushed wt=worktree " +
		"develop=unchecked main=default pushed=unchecked stale=unchecked web-wt=clean"
	if got := whys(items, web); got != want {
		t.Fatalf("what Inspect saw:\n got %s\nwant %s", got, want)
	}
	if ran, err := os.ReadFile(filepath.Join(home, "ran-web")); err == nil {
		t.Fatalf("looking at a repository ran a program that came with it:\n%s", ran)
	}
	repo, _ := r.Repo(web)
	if repo.Default != "main" || repo.Current != "main" || repo.Base != "refs/heads/main" || repo.GitBytes < 2<<20 {
		t.Fatalf("repo: %+v", repo)
	}
	if est := items[indexOf(items, web, RepoID)].Bytes; est < 1<<20 || est > 4<<20 {
		t.Fatalf("the estimate must be about the 2 MB only the gone branch holds, got %d", est)
	}

	// Ask origin: the stale branch shows up, the pushed one stays.
	have, err := r.CheckRemote(ctx, web)
	if err != nil || !have["pushed"] || have["stale"] || have["gone"] {
		t.Fatalf("origin's branches: %v %v", have, err)
	}
	items = ApplyRemote(items, web, have, nil)

	plan, err := r.Plan(Select(items, DefaultKeep, nil), recipe.Options{})
	if err != nil {
		t.Fatal(err)
	}
	engine.Prepare(&plan)
	before := scan.SizeOf(filepath.Join(web, ".git"))
	sha := run(t, web, "rev-parse", "gone")
	eng := &engine.Engine{Home: home, Volume: home, LogPath: filepath.Join(home, "log"),
		Trash: engine.DirTrash{Dir: filepath.Join(home, ".Trash")}}
	res := eng.Run(ctx, plan, nil)
	if res.Failed != -1 || len(res.FailedGroups()) != 0 || res.FinishedGroups() != 1 {
		log, _ := os.ReadFile(eng.LogPath)
		t.Fatalf("the run: %+v\n%s", res.Groups, log)
	}

	if got := branches(t, web); got != "develop main only wt origin/develop origin/main origin/pushed" {
		t.Fatalf("branches left: %s", got)
	}
	if n := len(strings.Split(run(t, web, "stash", "list"), "\n")); n != 2 {
		t.Fatalf("both stashes must survive, %d left", n)
	}
	if after := scan.SizeOf(filepath.Join(web, ".git")); before-after < 1<<20 {
		t.Fatalf(".git must shrink by the 2 MB of the gone branch: %d → %d", before, after)
	}
	if ran, err := os.ReadFile(filepath.Join(home, "ran-web")); err == nil {
		t.Fatalf("the plan ran a program that came with the repository:\n%s", ran)
	}
	// The untouched ones.
	if got := branches(t, dirty); got != "main old" {
		t.Fatalf("a repository with changes is left alone: %s", got)
	}
	if !exists(filepath.Join(work, "web-wt")) {
		t.Fatal("a worktree nobody marked stays")
	}

	// The backup brings the branch back, at the same commit.
	bundles, _ := filepath.Glob(filepath.Join(r.BackupDir(), "web-*.bundle"))
	if len(bundles) != 1 {
		t.Fatalf("one bundle for web, found %v", bundles)
	}
	run(t, web, "bundle", "verify", "-q", bundles[0])
	run(t, web, "fetch", "-q", bundles[0], "gone:gone", "refs/remotes/origin/stale:refs/remotes/origin/stale")
	if got := run(t, web, "rev-parse", "gone"); got != sha {
		t.Fatalf("the branch came back at %s, was %s", got, sha)
	}
	// The log keeps the commit of a merged branch, which has no backup.
	log, _ := os.ReadFile(eng.LogPath)
	if !strings.Contains(string(log), "Deleted branch merged (was ") {
		t.Fatalf("the log must say where the merged branch pointed:\n%s", log)
	}
}

func indexOf(items []recipe.Item, repo, id string) int {
	for i, it := range items {
		if it.Group == repo && it.ID == id {
			return i
		}
	}
	return -1
}

// What the user marks by hand: a branch that exists only here, and a clean
// worktree. The worktree goes to the Trash, and git forgets it.
func TestRealGitMarkedByHand(t *testing.T) {
	home, work := sandbox(t)
	ctx := context.Background()
	web := bigRepo(t, work, "web")
	r := newRecipe(t, home, web) // opened inside the repository
	items, err := r.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if repos := r.Repos(); len(repos) != 1 || repos[0].Name != "web" {
		t.Fatalf("inside a repository there is that one repository: %+v", repos)
	}
	wt := filepath.Join(work, "web-wt")
	marks := map[string]bool{
		MarkKey(items[indexOf(items, web, "b/only")]): true,
		MarkKey(items[indexOf(items, web, "w/"+wt)]):  true,
	}
	plan, err := r.Plan(Select(items, DefaultKeep, marks), recipe.Options{})
	if err != nil {
		t.Fatal(err)
	}
	engine.Prepare(&plan)
	trash := filepath.Join(home, ".Trash")
	res := (&engine.Engine{Home: home, Volume: home, Trash: engine.DirTrash{Dir: trash}}).Run(ctx, plan, nil)
	if len(res.FailedGroups()) != 0 || len(res.Failures()) != 0 {
		t.Fatalf("the run: %+v %+v", res.Groups, res.Failures())
	}
	if exists(wt) || !exists(filepath.Join(trash, "web-wt", "readme")) {
		t.Fatal("the marked worktree must be in the Trash")
	}
	if got := run(t, web, "worktree", "list", "--porcelain"); strings.Count(got, "worktree ") != 1 {
		t.Fatalf("git must forget the worktree:\n%s", got)
	}
	if got := branches(t, web); strings.Contains(got, "only") || !strings.Contains(got, "wt") {
		t.Fatalf("the marked branch goes, the worktree's branch stays: %s", got)
	}
	// The branch that existed only here is in the backup.
	bundles, _ := filepath.Glob(filepath.Join(r.BackupDir(), "*.bundle"))
	if len(bundles) != 1 || !strings.Contains(run(t, web, "bundle", "list-heads", bundles[0]), "refs/heads/only") {
		t.Fatalf("the backup must hold the branch: %v", bundles)
	}
}

// A home folder kept in git for its dotfiles is not a project: the
// repositories inside it are what gets listed.
func TestHomeInGitIsNotARepository(t *testing.T) {
	home, work := sandbox(t)
	run(t, home, "init", "-q", "-b", "main", home)
	repo := filepath.Join(work, "web")
	run(t, work, "init", "-q", "-b", "main", repo)
	commit(t, repo, "a", "a\n")
	for _, root := range []string{home, work} {
		r := newRecipe(t, home, root)
		if _, err := r.Inspect(context.Background()); err != nil {
			t.Fatal(err)
		}
		if repos := r.Repos(); len(repos) != 1 || repos[0].Path != repo {
			t.Fatalf("opened on %s: %+v", root, repos)
		}
	}
}

func TestNothingHere(t *testing.T) {
	home, work := sandbox(t)
	r := newRecipe(t, home, work)
	if _, err := r.Inspect(context.Background()); err != recipe.ErrNothingHere {
		t.Fatalf("a folder without repositories: %v", err)
	}
	if (&Recipe{}).Detect(context.Background(), recipe.Env{Home: home, Run: engine.Reader{}}) {
		t.Fatal("without an opened folder the recipe is not detected")
	}
}
