//go:build darwin

package screens

import (
	tea "charm.land/bubbletea/v2"

	"github.com/romus/space/internal/engine"
	"github.com/romus/space/internal/recipes/git"
	"github.com/romus/space/pkg/recipe"
)

// plan builds the plan for every included repository, or for one, and
// shows it. The switches of the plan screen rebuild it from the same
// selection; nothing is read from the repositories again.
func (g *Git) plan(only string) tea.Cmd {
	if g.rec == nil {
		return nil
	}
	if why := g.noPlan(only); why != "" {
		g.toast.set(why)
		return nil
	}
	sel := g.selection(only)
	opt := recipe.Options{Toggles: map[string]bool{
		git.TogglePrune: g.prune, git.ToggleCompact: g.pack, git.ToggleBackup: g.save,
	}}
	plan, err := g.rec.Plan(sel, opt)
	if err != nil {
		g.toast.set("No plan: " + err.Error())
		return nil
	}
	engine.Prepare(&plan)
	return push(NewPlan(g.c, PlanSpec{
		Plan:    plan,
		Crumbs:  []string{"Recipes", "Git · " + g.c.Tilde(g.c.Root)},
		Options: opt,
		Rebuild: func(o recipe.Options) (recipe.Plan, error) { return g.rec.Plan(sel, o) },
	}))
}

// skipped says why nothing can be done in a repository that is left alone.
func (g *Git) skipped(repo git.Repo) string {
	return repo.Name + " is left alone: " + repo.Skip + ". Nothing in it can be marked."
}

// noPlan says why there is nothing to plan, or "" when there is: the
// repository is left alone or was left out, or no repository is included.
func (g *Git) noPlan(only string) string {
	if only != "" {
		repo, _ := g.rec.Repo(only)
		switch {
		case repo.Skip != "":
			return "No plan: " + repo.Name + " is left alone — " + repo.Skip
		case !g.chosen(g.head(only)):
			return "No plan: " + repo.Name + " is left out — include it with ␣ on the list"
		}
		return ""
	}
	for _, repo := range g.repos {
		if g.chosen(g.head(repo.Path)) {
			return ""
		}
	}
	return "No plan: every repository here is left alone or left out"
}
