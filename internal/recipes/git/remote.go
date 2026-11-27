//go:build darwin

package git

import (
	"context"
	"maps"
	"strings"

	"github.com/romus/space/pkg/recipe"
)

// quiet makes git and ssh fail instead of asking: no password prompt, no
// passphrase prompt, and a short wait for a host that does not answer. The
// ssh command is set here, not taken from the repository's own settings.
var quiet = []string{"GIT_TERMINAL_PROMPT=0", "GIT_SSH_COMMAND=ssh -oBatchMode=yes -oConnectTimeout=5"}

// CheckRemote asks origin which branches it has. It reads and changes
// nothing: nothing is fetched. This is the one place the recipe uses the
// network, and only the interface calls it, never Inspect.
func (r *Recipe) CheckRemote(ctx context.Context, repo string) (map[string]bool, error) {
	args := append([]string{}, quiet...)
	args = append(args, "git", "-C", repo)
	args = append(args, guard...)
	args = append(args, readGuard...)
	args = append(args, "ls-remote", "--heads", "origin")
	out, err := r.env.Run.Output(ctx, "env", args...)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, line := range lines(string(out)) {
		if _, name, ok := strings.Cut(line, "\t"); ok {
			have[strings.TrimPrefix(name, heads)] = true
		}
	}
	return have, nil
}

// ApplyRemote brings a repository's items up to date with what origin
// answered: a remote-tracking branch that origin no longer has is stale and
// can go, and a local branch whose origin branch is gone is "gone". With an
// error nothing is known, and nothing remote is offered.
func ApplyRemote(items []recipe.Item, repo string, have map[string]bool, err error) []recipe.Item {
	out := make([]recipe.Item, len(items))
	for i, it := range items {
		if it.Group == repo {
			it.Meta = maps.Clone(it.Meta)
			applyRemote(&it, have, err)
		}
		out[i] = it
	}
	return out
}

func applyRemote(it *recipe.Item, have map[string]bool, err error) {
	name, onOrigin := strings.CutPrefix(it.Title, "origin/")
	switch it.Meta[MetaKind] {
	case KindRepo:
		it.Meta[MetaRemote] = RemoteChecked
		if err != nil {
			it.Meta[MetaRemote] = RemoteUnreachable
		}
	case KindRemote:
		if !onOrigin || it.Meta[MetaWhy] == WhyDefault {
			return
		}
		switch {
		case err != nil:
			it.Meta[MetaWhy], it.Locked, it.Default = WhyUnchecked, "origin did not answer", false
		case have[name]:
			it.Meta[MetaWhy], it.Locked, it.Default = WhyLive, "origin still has it", false
		default:
			it.Meta[MetaWhy], it.Locked, it.Default = WhyStale, "", true
		}
	case KindBranch:
		up, ok := strings.CutPrefix(it.Meta[MetaUpstream], "origin/")
		if err == nil && ok && !have[up] && it.Meta[MetaWhy] == WhyPushed {
			it.Meta[MetaWhy] = WhyGone
		}
	}
}
