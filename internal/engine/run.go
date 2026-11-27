//go:build darwin

package engine

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/romus/space/internal/humanize"
	"github.com/romus/space/internal/scan"
	"github.com/romus/space/pkg/recipe"
)

// run is one execution of a plan.
type run struct {
	e     *Engine
	plan  recipe.Plan
	exe   Executor
	log   io.WriteCloser
	sink  *sink
	res   Result // written through sink only, once the run has begun
	start time.Time
}

// Run executes the plan and reports progress through emit. Steps run one
// after another; groups of steps run side by side (see recipe.Step.Group).
// emit is never called by two goroutines at once.
func (e *Engine) Run(ctx context.Context, plan recipe.Plan, emit func(Event)) Result {
	r := e.begin(ctx, plan, emit)
	defer r.log.Close()
	base := 0
	for _, st := range Layout(plan) {
		if len(st.Groups) > 0 {
			r.groups(ctx, st.Groups, base)
			base += len(st.Groups)
			continue
		}
		if !r.serial(ctx, st.Step) {
			break
		}
	}
	return r.end(ctx)
}

// begin opens the log and takes the measurements made before anything runs.
func (e *Engine) begin(ctx context.Context, plan recipe.Plan, emit func(Event)) *run {
	if emit == nil {
		emit = func(Event) {}
	}
	r := &run{e: e, plan: plan, exe: e.Exec, log: e.openLog(), start: time.Now()}
	if r.exe == nil {
		r.exe = realExec{}
	}
	r.res = Result{Plan: plan, Steps: make([]StepResult, len(plan.Steps)), Failed: -1, LogPath: e.LogPath}
	for _, g := range Groups(plan) {
		r.res.Groups = append(r.res.Groups, GroupResult{Name: g.Name, Steps: g.Steps, Failed: -1})
	}
	r.sink = newSink(r.log, emit, &r.res)

	what := humanize.Plural(len(plan.Steps), "step")
	if n := len(r.res.Groups); n > 0 {
		what += fmt.Sprintf(" in %s, %d at a time", humanize.Plural(n, plan.Noun()), max(plan.Parallel, 1))
	}
	fmt.Fprintf(r.log, "\n=== %s  %s — %s\n", r.start.Format("2006-01-02 15:04:05"), plan.Title, what)

	r.res.FreeBefore = scan.FreeBytes(e.Volume)
	if e.Measure != nil {
		if label, n, ok := e.Measure(ctx); ok {
			r.res.Metric = &recipe.Metric{Label: label, Before: n, After: n}
		}
	}
	return r
}

// serial runs a step that stands on its own. It returns false when the run
// ends here: a stop was asked for, or the step failed.
func (r *run) serial(ctx context.Context, i int) bool {
	if r.halted(ctx) {
		r.sink.stoppedBefore(i)
		return false
	}
	sr := r.step(ctx, i)
	switch {
	case sr.Err == nil:
		return true
	case ctx.Err() != nil:
		// Cut short by the user, not broken.
		r.sink.cut()
	default:
		r.sink.failed(i, sr.Err)
	}
	return false
}

// halted reports whether nothing new may start.
func (r *run) halted(ctx context.Context) bool {
	return r.e.stop.Load() || ctx.Err() != nil
}

// end takes the measurements made after everything ran.
func (r *run) end(ctx context.Context) Result {
	r.res.FreeAfter = scan.FreeBytes(r.e.Volume)
	if r.res.Metric != nil {
		if _, n, ok := r.e.Measure(ctx); ok {
			r.res.Metric.After = n
		}
	}
	r.res.Elapsed = time.Since(r.start)
	fmt.Fprintf(r.log, "=== done in %s: free %s → %s\n", humanize.Duration(r.res.Elapsed),
		humanize.Bytes(r.res.FreeBefore), humanize.Bytes(r.res.FreeAfter))
	return r.res
}
