//go:build darwin

package engine

import (
	"time"

	"github.com/romus/space/pkg/recipe"
)

// Failure is one path a removal step could not remove, and why.
type Failure struct {
	Path   string
	Reason string // in plain words, ready to show
	Err    error
}

// StepResult is the outcome of one step.
type StepResult struct {
	// Failures are the paths a removal step had to leave behind. They do
	// not stop the plan: the other paths, and the steps after, still run.
	Failures []Failure
	Ran      bool
	Err      error
	Elapsed  time.Duration
	Bytes    int64 // bytes removed or moved, when the engine knows
	// Tail is the last lines a failed step printed: usually the tool's own
	// words for what went wrong. Empty when the step did not fail.
	Tail []string
}

// tailLines is how much of a failed step's output is kept in its result.
const tailLines = 5

// Result is the outcome of a whole run.
type Result struct {
	Plan       recipe.Plan
	Steps      []StepResult
	Stopped    bool // the user asked to stop between steps
	Failed     int  // index of the failed step, or -1
	Err        error
	FreeBefore int64
	FreeAfter  int64
	Trashed    int64 // bytes moved to the Trash: not reclaimed until it is emptied
	Metric     *recipe.Metric
	Elapsed    time.Duration
	LogPath    string
	// Groups is the outcome of every group, in plan order; nil for a plan
	// without groups. A group that fails does not set Failed or Err above:
	// those mean "the step that ended the run".
	Groups []GroupResult
}

// GroupResult is the outcome of one group of steps.
type GroupResult struct {
	Name    string
	Steps   []int         // indices into Plan.Steps
	Done    int           // steps that ran without error
	Failed  int           // index into Plan.Steps of the step that failed, or -1
	Err     error         // why the group failed
	Elapsed time.Duration // zero for a group that never started
}

// Finished reports whether every step of the group ran without error.
func (g GroupResult) Finished() bool { return g.Failed < 0 && g.Done == len(g.Steps) }

// FailedGroups lists the groups that a failing step ended.
func (r Result) FailedGroups() []GroupResult {
	var out []GroupResult
	for _, g := range r.Groups {
		if g.Failed >= 0 {
			out = append(out, g)
		}
	}
	return out
}

// FinishedGroups counts the groups that ran to the end.
func (r Result) FinishedGroups() int {
	n := 0
	for _, g := range r.Groups {
		if g.Finished() {
			n++
		}
	}
	return n
}

// Reclaimed is the space that really came back, never negative.
func (r Result) Reclaimed() int64 {
	if d := r.FreeAfter - r.FreeBefore; d > 0 {
		return d
	}
	return 0
}

// Failures lists every path that could not be removed, across all steps.
func (r Result) Failures() []Failure {
	var out []Failure
	for _, s := range r.Steps {
		out = append(out, s.Failures...)
	}
	return out
}

// Done counts the steps that ran without error.
func (r Result) Done() int {
	n := 0
	for _, s := range r.Steps {
		if s.Ran && s.Err == nil {
			n++
		}
	}
	return n
}
