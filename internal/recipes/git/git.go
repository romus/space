//go:build darwin

// Package git is the recipe for Git repositories: it finds every
// repository under the opened folder, works out which branches are safe to
// drop, and plans the deletion together with the compaction that actually
// gives the space back.
//
// Deleting a branch alone frees almost nothing: its commits stay in .git
// until the reflog forgets them and the repository is repacked. The plan
// does all of it in one go, the same steps in every repository.
package git

import (
	"context"
	"sync"

	"github.com/romus/space/internal/humanize"
	"github.com/romus/space/pkg/recipe"
)

// ID is the recipe's name on the command line.
const ID = "git"

// Parallel is how many repositories are cleaned at once.
const Parallel = 4

func init() { recipe.Register(ID, func() recipe.Recipe { return &Recipe{} }) }

// Recipe cleans the Git repositories under the opened folder.
type Recipe struct {
	env recipe.Env

	mu    sync.Mutex
	repos map[string]Repo // by path
	order []string        // paths, in the order they were found
}

func (r *Recipe) ID() string { return ID }

// Info has no Path on purpose: a repository stays an ordinary folder that
// can be moved to the Trash whole.
func (r *Recipe) Info() recipe.Info {
	return recipe.Info{
		Name:   "Git",
		Source: "built-in",
		Hint:   "git repository",
		Advice: "Deleting branches by hand frees almost nothing: their commits stay in .git.",
		About: "Finds the Git repositories under this folder and removes the branches that are merged, " +
			"gone on origin or pushed, then forgets them in the reflog and compacts .git. " +
			"Branches with commits that exist nowhere else are never picked for you. " +
			"What is deleted is saved to a bundle first and kept for 30 days.",
	}
}

// Detect is true when there is an opened folder and a git that works.
func (r *Recipe) Detect(ctx context.Context, env recipe.Env) bool {
	r.env = env
	if env.Root == "" || env.Run == nil {
		return false
	}
	path, err := env.Run.LookPath("git")
	if err != nil {
		return false
	}
	if path == "/usr/bin/git" {
		// Apple's stand-in is there on every Mac; without the developer
		// tools it only opens a window offering to install them.
		if _, err := env.Run.Output(ctx, "xcode-select", "-p"); err != nil {
			return false
		}
	}
	return true
}

// Repos returns the repositories found by the last Inspect, in order.
func (r *Recipe) Repos() []Repo {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Repo, 0, len(r.order))
	for _, p := range r.order {
		out = append(out, r.repos[p])
	}
	return out
}

// Repo returns one repository by its path.
func (r *Recipe) Repo(path string) (Repo, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	repo, ok := r.repos[path]
	return repo, ok
}

// Summary says what was found, in one line.
func (r *Recipe) Summary(items []recipe.Item) string {
	repos, branches := 0, 0
	for _, it := range items {
		if it.Meta[MetaKind] == KindRepo {
			repos++
		}
	}
	for _, it := range recipe.DefaultSelection(items) {
		if it.Meta[MetaKind] == KindBranch {
			branches++
		}
	}
	found := count(repos, "repository", "repositories")
	if branches == 0 {
		return found + " · no branches to remove, .git can be compacted"
	}
	return found + " · " + count(branches, "branch", "branches") + " to remove"
}

// count is a number with a noun whose plural is not just an added s.
func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return humanize.Count(int64(n)) + " " + many
}
