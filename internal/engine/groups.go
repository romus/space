//go:build darwin

package engine

import (
	"context"
	"errors"
	"sync"
	"time"
)

// groups runs one stage of groups, Plan.Parallel at a time. base is the
// index of the stage's first group in Result.Groups.
//
// The queue is filled and closed before any worker starts: groups begin in
// plan order, nothing blocks on a send, and a stop leaves nothing behind.
func (r *run) groups(ctx context.Context, gs []Group, base int) {
	queue := make(chan int, len(gs))
	for i := range gs {
		queue <- i
	}
	close(queue)

	var wg sync.WaitGroup
	for range min(max(r.plan.Parallel, 1), len(gs)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.groupWorker(ctx, gs, base, queue)
		}()
	}
	wg.Wait()
	r.sink.recap(base, len(gs))
}

// groupWorker takes groups off the queue until it is empty. It lives for
// one stage.
func (r *run) groupWorker(ctx context.Context, gs []Group, base int, queue <-chan int) {
	for i := range queue {
		r.group(ctx, base+i, gs[i])
	}
}

// group runs the steps of one group in order. A command that fails ends
// this group and no other. After a stop nothing new starts.
func (r *run) group(ctx context.Context, gi int, g Group) {
	gr := GroupResult{Name: g.Name, Steps: g.Steps, Failed: -1}
	if r.halted(ctx) {
		r.sink.groupFinished(gi, gr, true)
		return
	}
	if i, err := r.refused(gi, g); err != nil {
		gr.Failed, gr.Err = i, err
		r.sink.groupFinished(gi, gr, false)
		return
	}
	stopped := false
	t0 := time.Now()
	for _, i := range g.Steps {
		if r.halted(ctx) {
			stopped = true
			break
		}
		sr := r.step(ctx, i)
		if sr.Err == nil {
			gr.Done++
			continue
		}
		if ctx.Err() != nil {
			// Cut short by the user, not broken.
			stopped = true
		} else {
			gr.Failed, gr.Err = i, sr.Err
		}
		break
	}
	gr.Elapsed = time.Since(t0)
	r.sink.groupFinished(gi, gr, stopped)
}

// refused says why a group may not start at all, and which step is to
// blame. Two groups cannot share the terminal, so sudo is not allowed in
// one; and a group whose name already failed earlier in the plan stays
// stopped, so that nothing is compacted after a backup that did not work.
func (r *run) refused(gi int, g Group) (int, error) {
	for _, i := range g.Steps {
		if r.plan.Steps[i].Sudo {
			return i, errors.New("a step that needs sudo cannot run inside a group")
		}
	}
	if r.sink.failedEarlier(g.Name, gi) {
		return g.Steps[0], errors.New("skipped: an earlier step for it failed")
	}
	return -1, nil
}
