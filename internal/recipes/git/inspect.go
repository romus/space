//go:build darwin

package git

import (
	"context"
	"sync"

	"github.com/romus/space/pkg/recipe"
)

// inspectWorkers is how many repositories are read at once. Reading is a
// handful of quick git commands per repository.
const inspectWorkers = 8

// found is what reading one repository gave.
type found struct {
	repo  Repo
	items []recipe.Item
}

// Inspect finds the repositories under the opened folder and reads each:
// one item for the repository itself, then its branches, remote-tracking
// branches and worktrees. It only reads, and never talks to a remote.
func (r *Recipe) Inspect(ctx context.Context) ([]recipe.Item, error) {
	paths := find(ctx, r.env.Root, r.env.Home)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, recipe.ErrNothingHere
	}
	// The queue is filled and closed before the workers start, and every
	// worker writes only to its own slots of results.
	results := make([]found, len(paths))
	queue := make(chan int, len(paths))
	for i := range paths {
		queue <- i
	}
	close(queue)
	var wg sync.WaitGroup
	for range min(inspectWorkers, len(paths)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.inspectWorker(ctx, paths, results, queue)
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.repos, r.order = map[string]Repo{}, paths
	var items []recipe.Item
	for _, f := range results {
		r.repos[f.repo.Path] = f.repo
		items = append(items, f.items...)
	}
	return items, nil
}

// inspectWorker reads repositories off the queue until it is empty.
func (r *Recipe) inspectWorker(ctx context.Context, paths []string, results []found, queue <-chan int) {
	for i := range queue {
		if ctx.Err() != nil {
			return
		}
		repo, items := r.inspectRepo(ctx, paths[i])
		results[i] = found{repo, items}
	}
}
