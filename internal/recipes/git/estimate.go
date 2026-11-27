//go:build darwin

package git

import (
	"context"
	"strconv"
	"strings"

	"github.com/romus/space/pkg/recipe"
)

// Estimate is roughly what compacting a repository gives back once the
// selected branches are gone: the objects that only they reach, plus what
// git already counts as garbage. It is an estimate: repacking squeezes the
// rest tighter too, and what only the reflog still holds is not counted.
func (r *Recipe) Estimate(ctx context.Context, repo string, sel []recipe.Item) int64 {
	var drop, keep []string
	for _, it := range sel {
		if it.Group != repo {
			continue
		}
		switch it.Meta[MetaKind] {
		case KindBranch:
			drop = append(drop, heads+it.Title)
		case KindRemote:
			drop = append(drop, remotes+it.Title)
		}
	}
	total := r.garbage(ctx, repo)
	if len(drop) == 0 {
		return total
	}
	// Everything else that stays: every ref but the dropped ones, and HEAD.
	for _, name := range drop {
		keep = append(keep, "--exclude="+name)
	}
	args := append([]string{"rev-list", "--objects", "--disk-usage"}, drop...)
	args = append(args, "--not")
	args = append(args, keep...)
	args = append(args, "--all")
	if out, err := r.read(ctx, repo, args...); err == nil {
		n, _ := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
		total += n
	}
	return total
}

// garbage is what git itself reports as leftovers in the object store.
func (r *Recipe) garbage(ctx context.Context, repo string) int64 {
	out, err := r.read(ctx, repo, "count-objects", "-v")
	if err != nil {
		return 0
	}
	for _, line := range lines(out) {
		if v, ok := strings.CutPrefix(line, "size-garbage: "); ok {
			kib, _ := strconv.ParseInt(v, 10, 64)
			return kib * 1024
		}
	}
	return 0
}
