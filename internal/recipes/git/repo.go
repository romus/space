//go:build darwin

package git

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/romus/space/internal/scan"
	"github.com/romus/space/pkg/recipe"
)

// Keys and values of Item.Meta.
const (
	MetaKind     = "kind"
	MetaWhy      = "why"
	MetaOnly     = "only"     // commits that exist nowhere else
	MetaMerged   = "merged"   // "1": everything is in the main branch
	MetaUpstream = "upstream" // short name: origin/feature
	MetaBranch   = "branch"   // a worktree's branch
	MetaRemote   = "remote"   // on the repository item: what is known about origin

	KindRepo     = "repo"
	KindBranch   = "branch"
	KindRemote   = "remote"
	KindWorktree = "worktree"

	RemoteNone        = "none"        // the repository has no origin
	RemoteUnknown     = ""            // not asked yet
	RemoteChecked     = "checked"     // origin answered
	RemoteUnreachable = "unreachable" // origin did not answer
)

// RepoID is the ID of the item that stands for a whole repository.
const RepoID = "repo"

// Repo is what is known about one repository.
type Repo struct {
	Path     string
	Name     string // its path under the opened folder: one line, unique
	Origin   string // "github.com/me/web", or ""
	Default  string // the main branch
	Current  string // the branch checked out, "" when HEAD is detached
	GitBytes int64  // size of .git on disk
	LFSBytes int64  // size of .git/lfs, part of GitBytes
	Skip     string // why the whole repository is left alone, or ""
	Base     string // the ref that "merged" was measured against, in full
}

// repoName is the repository's path as seen from the opened folder.
func repoName(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		rel = filepath.Base(path)
	}
	return strings.Join(strings.Fields(rel), " ")
}

// inspectRepo reads one repository: its branches, worktrees and sizes.
func (r *Recipe) inspectRepo(ctx context.Context, path string) (Repo, []recipe.Item) {
	repo := Repo{Path: path, Name: repoName(r.env.Root, path)}
	gitDir := filepath.Join(path, ".git")
	repo.GitBytes = scan.SizeOf(gitDir)
	if _, err := os.Stat(filepath.Join(gitDir, "lfs")); err == nil {
		repo.LFSBytes = scan.SizeOf(filepath.Join(gitDir, "lfs"))
	}
	if url, err := r.read(ctx, path, "config", "--get", "remote.origin.url"); err == nil {
		repo.Origin = shortURL(url)
	}

	out, err := r.read(ctx, path, "for-each-ref", "--format="+refFormat, "refs/heads", "refs/remotes")
	refs := parseRefs(out)
	repo.Default = defaultBranch(refs)
	for _, rf := range refs {
		if rf.local() && rf.head {
			repo.Current = rf.short()
		}
	}
	repo.Skip = r.skip(ctx, path, repo, err)

	var merged map[string]bool
	repo.Base, merged = r.merged(ctx, path, refs, repo.Default)
	var items []recipe.Item
	for _, rf := range refs {
		switch {
		case rf.local():
			items = append(items, r.branchItem(ctx, path, rf, repo, merged))
		case rf.remote() && !strings.HasSuffix(rf.name, "/HEAD"):
			items = append(items, remoteItem(path, rf, repo.Default, merged))
		}
	}
	if wt, err := r.read(ctx, path, "worktree", "list", "--porcelain"); err == nil {
		items = append(items, r.worktreeItems(ctx, path, parseWorktrees(wt))...)
	}
	if repo.Skip != "" {
		for i := range items {
			items[i].Default = false
		}
	}
	head := recipe.Item{
		ID: RepoID, Group: path, Path: path, Title: repo.Name, Kind: recipe.Special,
		Locked: repo.Skip, Default: repo.Skip == "",
		Meta: map[string]string{MetaKind: KindRepo},
	}
	if repo.Origin == "" {
		head.Meta[MetaRemote] = RemoteNone
	}
	items = append([]recipe.Item{head}, items...)
	if repo.Skip == "" {
		items[0].Bytes = r.Estimate(ctx, path, Select(items, DefaultKeep, nil))
	}
	return repo, items
}

// skip says why a repository is left alone entirely, or "". A repository
// in the middle of something is not a place to delete branches and repack.
func (r *Recipe) skip(ctx context.Context, path string, repo Repo, refsErr error) string {
	gitDir := filepath.Join(path, ".git")
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(gitDir, name))
		return err == nil
	}
	switch {
	case refsErr != nil:
		return "git could not read it"
	case exists("index.lock") || exists("gc.pid"):
		return "git is running here"
	case exists("rebase-merge") || exists("rebase-apply"):
		return "rebase in progress"
	case exists("MERGE_HEAD") || exists("CHERRY_PICK_HEAD") || exists("BISECT_LOG"):
		return "merge in progress"
	case repo.Current == "":
		return "detached HEAD"
	}
	// Untracked files do not count: nothing here touches the working files,
	// and nearly every repository has some.
	if out, err := r.read(ctx, path, "status", "--porcelain", "--untracked-files=no"); err != nil || out != "" {
		return "uncommitted changes"
	}
	return ""
}

// merged is the set of refs whose every commit is in the main branch, and
// the ref that stands for the main branch: the local one, else origin's.
func (r *Recipe) merged(ctx context.Context, path string, refs []ref, def string) (string, map[string]bool) {
	base := ""
	for _, rf := range refs {
		if rf.name == heads+def || (base == "" && rf.name == remotes+"origin/"+def) {
			base = rf.name
		}
	}
	set := map[string]bool{}
	if base == "" {
		return "", set
	}
	out, err := r.read(ctx, path, "for-each-ref", "--merged="+base, "--format=%(refname)", "refs/heads", "refs/remotes")
	if err != nil {
		return base, set
	}
	for _, name := range lines(out) {
		set[name] = true
	}
	return base, set
}

func (r *Recipe) branchItem(ctx context.Context, path string, rf ref, repo Repo, merged map[string]bool) recipe.Item {
	def := repo.Default
	only := 0
	if rf.upstream == "" && !merged[rf.name] {
		// Never pushed under this name: how many of its commits are neither
		// on a remote nor in the main branch?
		args := []string{"rev-list", "--count", rf.name, "--not", "--remotes"}
		if repo.Base != "" {
			args = append(args, repo.Base)
		}
		if out, err := r.read(ctx, path, args...); err == nil {
			only, _ = strconv.Atoi(out)
		}
	}
	reason, n := why(rf, def, merged, only)
	it := recipe.Item{
		ID: "b/" + rf.short(), Group: path, Title: rf.short(), Kind: recipe.Special, ModTime: rf.when,
		Meta: map[string]string{MetaKind: KindBranch, MetaWhy: reason},
	}
	if n > 0 {
		it.Meta[MetaOnly] = strconv.Itoa(n)
	}
	if merged[rf.name] {
		it.Meta[MetaMerged] = "1"
	}
	if rf.upstream != "" {
		it.Meta[MetaUpstream] = strings.TrimPrefix(rf.upstream, remotes)
	}
	switch reason {
	case WhyDefault:
		it.Locked = "the main branch"
	case WhyCurrent:
		it.Locked = "checked out here"
	case WhyWorktree:
		it.Locked = "checked out in " + rf.worktree
		it.Path = rf.worktree
	}
	it.Default = it.Locked == "" && reason != WhyOnly && !Kept(rf.short(), DefaultKeep)
	return it
}

// The states of a remote-tracking branch, stored in Item.Meta[MetaWhy].
const (
	WhyUnchecked = "unchecked" // origin was not asked yet
	WhyLive      = "live"      // origin still has the branch
	WhyStale     = "stale"     // origin no longer has it
)

// remoteItem describes a remote-tracking branch. Whether it is stale is
// only known once origin was asked (see CheckRemote).
func remoteItem(path string, rf ref, def string, merged map[string]bool) recipe.Item {
	it := recipe.Item{
		ID: "r/" + rf.short(), Group: path, Title: rf.short(), Kind: recipe.Special, ModTime: rf.when,
		Locked: "origin was not asked yet",
		Meta:   map[string]string{MetaKind: KindRemote, MetaWhy: WhyUnchecked},
	}
	if merged[rf.name] {
		it.Meta[MetaMerged] = "1"
	}
	if rf.short() == "origin/"+def {
		it.Meta[MetaWhy], it.Locked = WhyDefault, "the main branch on origin"
	}
	if !strings.HasPrefix(rf.short(), "origin/") {
		it.Locked = "not on origin"
	}
	return it
}

// shortURL turns a remote URL into host/path: "github.com/me/web".
func shortURL(url string) string {
	url = strings.TrimSuffix(strings.TrimSpace(url), ".git")
	if _, rest, ok := strings.Cut(url, "://"); ok {
		url = rest // https://me@github.com/me/web
	} else if host, path, ok := strings.Cut(url, ":"); ok && !strings.Contains(host, "/") {
		url = host + "/" + path // git@github.com:me/web
	}
	if at := strings.Index(url, "@"); at >= 0 && !strings.Contains(url[:at], "/") {
		url = url[at+1:]
	}
	return url
}
