//go:build darwin

package git

import (
	"path/filepath"
	"time"

	"github.com/romus/space/internal/humanize"
	"github.com/romus/space/pkg/recipe"
)

// The names the steps fold under on the plan screen.
const (
	kindBackup    = "Back up branches"
	kindDelete    = "Delete branches"
	kindPrune     = "Prune stale refs"
	kindWorktrees = "Remove worktrees"
	kindForget    = "Forget old history"
	kindCompact   = "Compact"
	kindLFS       = "Prune the LFS cache"
)

// repoSteps is the plan for one repository, in the order that keeps it
// safe: save, delete, and only then forget and repack.
func (r *Recipe) repoSteps(p *picked, total sums, compact, backup bool, now time.Time) []recipe.Step {
	repo := p.repo
	step := func(s recipe.Step) recipe.Step {
		s.Group = repo.Name
		return s
	}
	var steps []recipe.Step

	// What needs a backup: branches whose commits would be out of reach.
	// A merged branch needs none, its commits stay; nor does a pushed one,
	// which origin's own branch still holds. The log keeps the commit each
	// deleted branch pointed at.
	var save []string
	for _, it := range p.branches {
		if w := it.Meta[MetaWhy]; w == WhyGone || w == WhyOnly {
			save = append(save, heads+it.Title)
		}
	}
	for _, it := range p.remotes {
		if it.Meta[MetaMerged] == "" {
			save = append(save, remotes+it.Title)
		}
	}
	// Without a backup, deleting what is not merged cannot be undone once
	// the history is forgotten.
	undo := backup || len(save) == 0
	lost := "The log keeps the commit of each."
	if !undo {
		lost = "Not backed up: the unmerged ones are gone for good once the history is forgotten."
	}
	if backup && len(save) > 0 {
		file := r.bundlePath(repo, now)
		args := append([]string{"bundle", "create", file}, save...)
		if repo.Base != "" {
			args = append(args, "--not", repo.Base)
		}
		steps = append(steps, step(recipe.Step{
			Title: "Back up " + count(len(save), "branch", "branches"), Safe: true,
			Kind: kind(kindBackup, "one file per repo, kept 30 days"),
			Why:  "To bring one back: git fetch " + humanize.Tilde(file, r.env.Home) + " <branch>:<branch>",
			Cmd:  stepCmd(repo.Path, args...),
		}))
	}
	if len(p.branches) > 0 {
		steps = append(steps, step(recipe.Step{
			Title: "Delete " + count(len(p.branches), "branch", "branches"), Reversible: undo,
			Kind: kind(kindDelete, "%s in total", humanize.Count(int64(total.branches))),
			Why:  "Merged, gone on origin, pushed, or marked by you. " + lost,
			Cmd:  stepCmd(repo.Path, append([]string{"branch", "-D"}, titles(p.branches)...)...),
		}))
	}
	if len(p.remotes) > 0 {
		steps = append(steps, step(recipe.Step{
			Title: "Prune " + humanize.Plural(len(p.remotes), "stale origin ref"), Reversible: undo,
			Kind: kind(kindPrune, "origin/* that origin no longer has"),
			Why:  "Only the local record of branches that were deleted on origin.",
			Cmd:  stepCmd(repo.Path, append([]string{"branch", "-d", "-r"}, titles(p.remotes)...)...),
		}))
	}
	steps = append(steps, r.worktreeSteps(p, step)...)
	if !compact {
		return steps
	}
	steps = append(steps,
		step(recipe.Step{
			Title: "Forget old history",
			Kind:  kind(kindForget, "the reflog keeps deleted commits alive"),
			Why:   "Commits that no branch reaches can no longer be found by git reflog. Stashes stay.",
			Cmd:   stepCmd(repo.Path, "-c", "gc.reflogExpireUnreachable=now", "reflog", "expire", "--all"),
		}),
		step(recipe.Step{
			Title: "Compact", Safe: true,
			Kind: kind(kindCompact, "this is where space comes back"),
			Why:  "Repacks what is left and drops what nothing points to.",
			Cmd:  stepCmd(repo.Path, "gc", "--prune=now"),
		}))
	if repo.LFSBytes > 0 && r.hasLFS() {
		steps = append(steps, step(recipe.Step{
			Title: "Prune the LFS cache", Safe: true,
			Kind: kindLFS,
			Why:  "Large files that are on the server and not needed by recent commits.",
			Cmd:  stepCmd(repo.Path, "lfs", "prune"),
		}))
	}
	return steps
}

// worktreeSteps moves the marked worktrees to the Trash, where they can
// be put back from, and lets git forget the ones that are gone.
func (r *Recipe) worktreeSteps(p *picked, step func(recipe.Step) recipe.Step) []recipe.Step {
	if len(p.worktrees) == 0 {
		return nil
	}
	var steps []recipe.Step
	for _, it := range p.worktrees {
		if it.Meta[MetaWhy] == WhyMissing {
			continue
		}
		steps = append(steps, step(recipe.Step{
			Title: "Move worktree " + filepath.Base(it.Path) + " to the Trash", Reversible: true,
			Kind: kind(kindWorktrees, "to the Trash"),
			Why:  "A clean worktree: everything in it is committed.",
			Remove: &recipe.Remove{
				Paths: []string{it.Path}, ToTrash: true, Root: filepath.Dir(it.Path), Bytes: it.Bytes,
			},
		}))
	}
	return append(steps, step(recipe.Step{
		Title: "Forget removed worktrees", Safe: true,
		Kind: kind(kindWorktrees, "to the Trash"),
		Why:  "Drops git's record of worktrees whose folder is gone.",
		Cmd:  stepCmd(p.repo.Path, "worktree", "prune"),
	}))
}

func titles(items []recipe.Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Title
	}
	return out
}

// hasLFS reports whether git-lfs is installed.
func (r *Recipe) hasLFS() bool {
	_, err := r.env.Run.LookPath("git-lfs")
	return err == nil
}
