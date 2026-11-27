//go:build darwin

package git

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/romus/space/internal/humanize"
	"github.com/romus/space/pkg/recipe"
)

// The switches of the plan.
const (
	TogglePrune   = "prune"   // drop the origin/* refs origin no longer has
	ToggleCompact = "compact" // forget in the reflog and repack
	ToggleBackup  = "backup"  // save what is not merged to a bundle first
)

// picked is what a plan takes from one repository.
type picked struct {
	repo      Repo
	head      recipe.Item
	branches  []recipe.Item
	remotes   []recipe.Item
	worktrees []recipe.Item
}

// Plan turns a selection into steps: the same few steps in each
// repository, as one group per repository. It works from what Inspect
// read and runs nothing.
func (r *Recipe) Plan(sel []recipe.Item, opt recipe.Options) (recipe.Plan, error) {
	prune, compact := opt.Toggle(TogglePrune, true), opt.Toggle(ToggleCompact, true)
	backup := opt.Toggle(ToggleBackup, true)
	picks := r.pick(sel, prune)
	if len(picks) == 0 {
		return recipe.Plan{}, errors.New("no repository is included")
	}
	now := r.env.Now()
	total := totals(picks)
	plan := recipe.Plan{
		RecipeID: ID, Title: "Git", Parallel: Parallel, GroupNoun: "repo",
		Toggles: []recipe.Toggle{
			{Key: TogglePrune, Label: "prune stale origin refs", On: prune},
			{Key: ToggleCompact, Label: "compact .git", On: compact},
			{Key: ToggleBackup, Label: "back up first", On: backup},
		},
	}
	var steps []recipe.Step
	backups, seconds, repos := false, 0, 0
	for _, p := range picks {
		own := r.repoSteps(p, total, compact, backup, now)
		if len(own) == 0 {
			continue
		}
		repos++
		backups = backups || strings.HasPrefix(own[0].Kind, kindBackup)
		steps = append(steps, own...)
		seconds += 3 + int(p.repo.GitBytes/100e6)
		if compact {
			plan.Reclaim += p.head.Bytes
		}
		plan.ToTrash += recipe.Sum(p.worktrees)
	}
	if len(steps) == 0 {
		return recipe.Plan{}, errors.New("nothing to do: no branch is marked and compacting is off")
	}
	plan.Steps = append(r.before(backups, now), steps...)
	plan.Notes = r.notes(backups)
	plan.Seconds = max(5, seconds/min(Parallel, repos))
	plan.Summary = []string{
		"repositories\t" + humanize.Count(int64(repos)),
		"branches deleted\t" + humanize.Count(int64(total.branches)),
	}
	if backups {
		plan.Summary = append(plan.Summary, "backups\t"+humanize.Tilde(r.BackupDir(), r.env.Home)+" · kept 30 days")
	}
	return plan, nil
}

// pick sorts a selection by repository.
func (r *Recipe) pick(sel []recipe.Item, prune bool) []*picked {
	by := map[string]*picked{}
	var order []*picked
	for _, it := range sel {
		if it.Meta[MetaKind] != KindRepo || it.Locked != "" {
			continue
		}
		if repo, ok := r.Repo(it.Group); ok && by[it.Group] == nil {
			by[it.Group] = &picked{repo: repo, head: it}
			order = append(order, by[it.Group])
		}
	}
	// The biggest first: they take the longest, and it is the order of the
	// list the user chose from.
	sort.SliceStable(order, func(i, j int) bool { return order[i].repo.GitBytes > order[j].repo.GitBytes })
	for _, it := range sel {
		p := by[it.Group]
		if p == nil || it.Locked != "" {
			continue
		}
		switch it.Meta[MetaKind] {
		case KindBranch:
			p.branches = append(p.branches, it)
		case KindRemote:
			if prune {
				p.remotes = append(p.remotes, it)
			}
		case KindWorktree:
			p.worktrees = append(p.worktrees, it)
		}
	}
	return order
}

type sums struct{ branches, remotes, worktrees int }

func totals(picks []*picked) sums {
	var s sums
	for _, p := range picks {
		s.branches += len(p.branches)
		s.remotes += len(p.remotes)
		s.worktrees += len(p.worktrees)
	}
	return s
}

// before is what runs once, ahead of the repositories: the folder for the
// backups, and the backups that are past their time.
func (r *Recipe) before(backups bool, now time.Time) []recipe.Step {
	var steps []recipe.Step
	if backups {
		steps = append(steps, recipe.Step{
			Title: "Create the backup folder", Safe: true,
			Why: "Deleted branches are saved there as bundles and kept for 30 days.",
			Cmd: []string{"mkdir", "-p", r.BackupDir()},
		})
	}
	if old := r.oldBackups(now); len(old) > 0 {
		steps = append(steps, recipe.Step{
			Title: "Remove " + humanize.Plural(len(old), "backup") + " older than 30 days", Reversible: true,
			Why:    "Bundles of branches deleted more than 30 days ago.",
			Remove: &recipe.Remove{Paths: old, ToTrash: true, Root: r.BackupDir()},
		})
	}
	return steps
}

// notes are the lines under the folded plan: what is skipped, where the
// backups go, and what the plan never touches.
func (r *Recipe) notes(backups bool) []string {
	var skipped []string
	for _, repo := range r.Repos() {
		if repo.Skip != "" {
			skipped = append(skipped, repo.Name+" — "+repo.Skip)
		}
	}
	var notes []string
	if len(skipped) > 0 {
		notes = append(notes, "skipped\t"+strings.Join(skipped, " · "))
	}
	if backups {
		// A backup holds what it saves: that part of the space is not back
		// until the backup goes.
		notes = append(notes, "backups\tkept 30 days in "+humanize.Tilde(r.BackupDir(), r.env.Home)+
			" — the space of unmerged branches comes back then")
	}
	return append(notes, "untouched\tbranches on origin · stashes · tags · working files")
}

func kind(title, note string, args ...any) string {
	if note == "" {
		return title
	}
	return title + "\t" + fmt.Sprintf(note, args...)
}
