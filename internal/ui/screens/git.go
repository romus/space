//go:build darwin

package screens

import (
	"context"
	"sort"

	tea "charm.land/bubbletea/v2"

	"github.com/romus/space/internal/recipes/git"
	"github.com/romus/space/pkg/recipe"
)

// The tabs of one repository.
const (
	gitLocal = iota
	gitRemote
	gitWorktrees
)

// gitJobs is how many background questions run at once: asking an origin
// for its branches, or git for an estimate.
const gitJobs = 4

// Git lets the user choose repositories and branches to clean. It has two
// views: every repository under the opened folder, and one repository with
// its branches. Opened inside a repository, it shows only the second.
type Git struct {
	c   *Ctx
	rs  *RecipeState
	rec *git.Recipe

	repos []git.Repo        // in the order shown
	skip  map[string]string // why a repository is left alone, by path
	keep  []string          // the rule: branch names that stay
	marks map[string]bool   // what the user set by hand
	prune bool              // drop stale origin refs
	pack  bool              // forget in the reflog and compact
	save  bool              // back up what is not merged first

	cur, off   int
	open       int // the repository shown on its own, or -1 for the list
	single     bool
	tab        int
	rcur, roff int
	expanded   map[string]bool // folds of branches opened with →

	editing bool
	input   string

	est     map[string]int64 // estimates worked out after the marks changed
	gen     map[string]int   // to drop an estimate that came too late
	todo    []tea.Cmd        // background questions not asked yet
	running int
	asking  map[string]bool // origins that have not answered yet

	toast toast
}

// gitRemoteMsg is an origin's answer: the branches it has.
type gitRemoteMsg struct {
	repo string
	have map[string]bool
	err  error
}

// gitEstimateMsg is a fresh estimate for one repository.
type gitEstimateMsg struct {
	repo  string
	bytes int64
	gen   int
}

// NewGit opens the Git recipe.
func NewGit(c *Ctx, rs *RecipeState) *Git {
	g := &Git{
		c: c, rs: rs, open: -1, prune: true, pack: true, save: true,
		marks: map[string]bool{}, expanded: map[string]bool{},
		est: map[string]int64{}, gen: map[string]int{}, asking: map[string]bool{},
	}
	g.rec, _ = rs.R.(*git.Recipe)
	g.keep = c.State.GitKeep
	if len(g.keep) == 0 {
		g.keep = git.DefaultKeep
	}
	if g.rec != nil {
		g.repos = g.rec.Repos()
	}
	g.skip = map[string]string{}
	for _, repo := range g.repos {
		g.skip[repo.Path] = repo.Skip
	}
	// Size is the hierarchy; what is skipped goes last.
	sort.SliceStable(g.repos, func(i, j int) bool {
		a, b := g.repos[i], g.repos[j]
		if (a.Skip == "") != (b.Skip == "") {
			return a.Skip == ""
		}
		return a.GitBytes > b.GitBytes
	})
	if len(g.repos) == 1 && g.repos[0].Path != "" && insideOf(c.Root, g.repos[0].Path) {
		g.single, g.open = true, 0
	}
	return g
}

// insideOf reports whether dir is the folder root or lies inside it.
func insideOf(dir, root string) bool {
	return dir == root || len(dir) > len(root) && dir[:len(root)+1] == root+"/"
}

func (g *Git) Title() string { return "git" }
func (g *Git) Typing() bool  { return g.editing }

// Start asks the origins which branches they still have. Nothing is
// fetched, and nothing is asked twice: the answer stays with the items.
func (g *Git) Start() tea.Cmd {
	if g.rec == nil {
		return nil
	}
	for _, repo := range g.repos {
		if repo.Skip != "" || g.head(repo.Path).Meta[git.MetaRemote] != git.RemoteUnknown {
			continue
		}
		path := repo.Path
		g.asking[path] = true
		g.todo = append(g.todo, func() tea.Msg {
			have, err := g.rec.CheckRemote(context.Background(), path)
			return gitRemoteMsg{repo: path, have: have, err: err}
		})
	}
	return g.pump()
}

// pump starts waiting questions, never more than gitJobs at once.
func (g *Git) pump() tea.Cmd {
	var cmds []tea.Cmd
	for g.running < gitJobs && len(g.todo) > 0 {
		cmds = append(cmds, g.todo[0])
		g.todo = g.todo[1:]
		g.running++
	}
	return tea.Batch(cmds...)
}

// estimate asks git again how much a repository would give back, after
// what is chosen in it changed.
func (g *Git) estimate(repo string) {
	if g.rec == nil {
		return
	}
	g.gen[repo]++
	gen := g.gen[repo]
	sel := git.Select(g.rs.Items, g.keep, g.marks)
	g.todo = append(g.todo, func() tea.Msg {
		return gitEstimateMsg{repo: repo, bytes: g.rec.Estimate(context.Background(), repo, sel), gen: gen}
	})
}

// --- what is chosen ---------------------------------------------------------

// head is the item that stands for a whole repository.
func (g *Git) head(repo string) recipe.Item {
	for _, it := range g.rs.Items {
		if it.Group == repo && it.ID == git.RepoID {
			return it
		}
	}
	return recipe.Item{Meta: map[string]string{}}
}

// of lists a repository's items of one kind.
func (g *Git) of(repo, kind string) []recipe.Item {
	var out []recipe.Item
	for _, it := range g.rs.Items {
		if it.Group == repo && it.Meta[git.MetaKind] == kind {
			out = append(out, it)
		}
	}
	return out
}

// chosen reports whether an item is taken. Nothing is taken from a
// repository that is left alone, whatever is marked in it.
func (g *Git) chosen(it recipe.Item) bool {
	if it.ID != git.RepoID && g.skip[it.Group] != "" {
		return false
	}
	return git.Chosen(it, g.keep, g.marks)
}

// taken counts the chosen items of one kind in a repository.
func (g *Git) taken(repo, kind string) int {
	n := 0
	for _, it := range g.of(repo, kind) {
		if g.chosen(it) {
			n++
		}
	}
	return n
}

// reclaim is the estimate for a repository as it is chosen now.
func (g *Git) reclaim(repo string) int64 {
	if n, ok := g.est[repo]; ok {
		return n
	}
	return g.head(repo).Bytes
}

// selection is what a plan takes: every included repository, or only one.
// The repository items carry the current estimates.
func (g *Git) selection(only string) []recipe.Item {
	var sel []recipe.Item
	for _, it := range git.Select(g.rs.Items, g.keep, g.marks) {
		if only != "" && it.Group != only {
			continue
		}
		if it.ID == git.RepoID {
			it.Bytes = g.reclaim(it.Group)
		}
		sel = append(sel, it)
	}
	return sel
}
