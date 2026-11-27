//go:build darwin

package git

import (
	"context"
	"strings"
)

// space runs git in every repository under the opened folder, including
// ones that were downloaded from somewhere. A repository can name programs
// for git to run: hooks, and a file system monitor that git starts whenever
// it reads the index — for "git status", and for "git gc" as well. Both are
// switched off on every call, so that neither looking at a folder nor
// cleaning it runs code that came with it.
var guard = []string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false"}

// readGuard is added to the commands that only look: they take no locks,
// so they never get in the way of a git the user is running.
var readGuard = []string{"--no-optional-locks"}

// stepCmd is the argv of a plan step: git, in repo, with the guard. This is
// the only place the commands of a plan are put together.
func stepCmd(repo string, args ...string) []string {
	argv := append([]string{"git", "-C", repo}, guard...)
	return append(argv, args...)
}

// read runs a git command that only looks, and returns what it printed.
func (r *Recipe) read(ctx context.Context, repo string, args ...string) (string, error) {
	argv := append([]string{"-C", repo}, guard...)
	argv = append(argv, readGuard...)
	out, err := r.env.Run.Output(ctx, "git", append(argv, args...)...)
	return strings.TrimRight(string(out), "\n"), err
}

// lines splits output into its non-empty lines.
func lines(out string) []string {
	var res []string
	for _, l := range strings.Split(out, "\n") {
		if l != "" {
			res = append(res, l)
		}
	}
	return res
}
