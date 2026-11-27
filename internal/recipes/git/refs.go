//go:build darwin

package git

import (
	"strconv"
	"strings"
	"time"
)

// refFormat asks git for one ref per line, fields split by tabs.
const refFormat = "%(refname)%09%(objectname)%09%(committerdate:unix)%09%(upstream)%09%(upstream:track)%09%(HEAD)%09%(worktreepath)%09%(symref)"

const (
	heads   = "refs/heads/"
	remotes = "refs/remotes/"
)

// ref is one branch or remote-tracking branch as git describes it.
type ref struct {
	name     string // full: refs/heads/main
	sha      string
	when     time.Time
	upstream string // full: refs/remotes/origin/main
	track    string // "[gone]", "[ahead 2]", ""
	head     bool   // checked out here
	worktree string // path of the worktree that has it checked out
	symref   string // target, for origin/HEAD
}

func (r ref) local() bool  { return strings.HasPrefix(r.name, heads) }
func (r ref) remote() bool { return strings.HasPrefix(r.name, remotes) }

// short is the name as people type it: "main", "origin/main".
func (r ref) short() string {
	return strings.TrimPrefix(strings.TrimPrefix(r.name, heads), remotes)
}

// ahead is how many commits the branch has that its upstream lacks.
func (r ref) ahead() int {
	_, rest, ok := strings.Cut(r.track, "ahead ")
	if !ok {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimRight(strings.SplitN(rest, ",", 2)[0], "]"))
	return n
}

// parseRefs reads the output of for-each-ref with refFormat.
func parseRefs(out string) []ref {
	var refs []ref
	for _, line := range lines(out) {
		f := strings.Split(line, "\t")
		if len(f) < 8 {
			continue
		}
		unix, _ := strconv.ParseInt(f[2], 10, 64)
		refs = append(refs, ref{
			name: f[0], sha: f[1], when: time.Unix(unix, 0),
			upstream: f[3], track: f[4], head: f[5] == "*", worktree: f[6], symref: f[7],
		})
	}
	return refs
}

// defaultBranch works out the repository's main branch: what origin says
// its HEAD is, else main or master, else the branch that is checked out.
func defaultBranch(refs []ref) string {
	have := map[string]bool{}
	current := ""
	for _, r := range refs {
		if r.name == remotes+"origin/HEAD" && strings.HasPrefix(r.symref, remotes+"origin/") {
			return strings.TrimPrefix(r.symref, remotes+"origin/")
		}
		if r.local() {
			have[r.short()] = true
			if r.head {
				current = r.short()
			}
		}
	}
	for _, name := range []string{"main", "master"} {
		if have[name] {
			return name
		}
	}
	return current
}

// The reasons a branch is kept or offered, stored in Item.Meta[MetaWhy].
const (
	WhyDefault  = "default"  // the main branch
	WhyCurrent  = "current"  // checked out here
	WhyWorktree = "worktree" // checked out in a linked worktree
	WhyMerged   = "merged"   // everything is in the main branch
	WhyGone     = "gone"     // its branch on origin was deleted
	WhyPushed   = "pushed"   // every commit is on origin
	WhyOnly     = "only"     // has commits that exist nowhere else
)

// why classifies a local branch. only is the number of its commits that
// are on no remote, when that had to be counted.
func why(r ref, def string, merged map[string]bool, only int) (reason string, n int) {
	switch {
	case r.short() == def:
		return WhyDefault, 0
	case r.head:
		return WhyCurrent, 0
	case r.worktree != "":
		return WhyWorktree, 0
	case merged[r.name]:
		return WhyMerged, 0
	case r.track == "[gone]":
		return WhyGone, 0
	case r.upstream != "" && r.ahead() > 0:
		return WhyOnly, r.ahead()
	case r.upstream != "":
		return WhyPushed, 0
	case only > 0:
		return WhyOnly, only
	}
	return WhyPushed, 0
}
