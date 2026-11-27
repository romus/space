//go:build darwin

package engine

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/romus/space/internal/humanize"
	"github.com/romus/space/pkg/recipe"
)

// sink is the one place a run reports to: the log, the caller's events and
// the result. Groups run side by side, so everything goes through one lock:
// emit is never called by two goroutines, a log line is never torn, and the
// log and the events agree on the order of things.
type sink struct {
	mu   sync.Mutex
	log  io.Writer
	emit func(Event)
	res  *Result
	plan recipe.Plan
	pad  int // width of the group column in the log
}

func newSink(log io.Writer, emit func(Event), res *Result) *sink {
	s := &sink{log: log, emit: emit, res: res, plan: res.Plan}
	for _, g := range res.Groups {
		s.pad = max(s.pad, len(g.Name))
	}
	s.pad = min(s.pad, 24)
	return s
}

// logf writes whole lines, each with the group's column in front, as one
// write. Lines of a step outside any group have no column, so the log of a
// plan without groups reads exactly as it always did. The caller holds mu.
func (s *sink) logf(group, format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	if group == "" {
		fmt.Fprintln(s.log, text)
		return
	}
	prefix := fmt.Sprintf("%-*s | ", s.pad, oneLine(group))
	var b strings.Builder
	for _, line := range strings.Split(text, "\n") {
		b.WriteString(prefix + line + "\n")
	}
	io.WriteString(s.log, b.String())
}

// oneLine keeps a name from breaking a line of the log or of a script.
func oneLine(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' }), " ")
}

func (s *sink) started(i int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	step := s.plan.Steps[i]
	s.emit(StepStarted{Index: i})
	s.logf(step.Group, "--- step %d: %s\n$ %s", i+1, step.Title, Display(step, ""))
}

func (s *sink) output(i int, line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logf(s.plan.Steps[i].Group, "%s", line)
	s.emit(StepOutput{Index: i, Line: line})
}

// finished records a step's result; trashed is what it moved to the Trash.
func (s *sink) finished(i int, sr StepResult, trashed int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.res.Steps[i] = sr
	s.res.Trashed += trashed
	if sr.Err != nil {
		s.logf(s.plan.Steps[i].Group, "!!! step %d failed: %v", i+1, sr.Err)
	}
	s.emit(StepFinished{Index: i, Result: sr})
}

// failed marks the step that ended the run.
func (s *sink) failed(i int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.res.Failed, s.res.Err = i, err
}

// stoppedBefore notes that a stop kept step i from starting.
func (s *sink) stoppedBefore(i int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.res.Stopped = true
	s.logf("", "--- stopped before step %d", i+1)
}

// cut notes that the user cut a running step short.
func (s *sink) cut() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.res.Stopped = true
}

// failedEarlier reports whether a group of this name failed before group gi.
func (s *sink) failedEarlier(name string, gi int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.res.Groups[:gi] {
		if g.Name == name && g.Failed >= 0 {
			return true
		}
	}
	return false
}

// groupFinished records how a group ended. stopped means a stop, not a
// failure, left it unfinished.
func (s *sink) groupFinished(gi int, gr GroupResult, stopped bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.res.Groups[gi] = gr
	left := len(gr.Steps) - gr.Done
	switch {
	case gr.Finished():
		s.logf(gr.Name, "--- finished in %s", humanize.Duration(gr.Elapsed))
	case stopped:
		s.res.Stopped = true
		s.logf(gr.Name, "--- stopped: %s not run", humanize.Plural(left, "step"))
	default:
		if !s.res.Steps[gr.Failed].Ran {
			s.logf(gr.Name, "!!! %v", gr.Err)
		}
		s.logf(gr.Name, "--- not finished: %s left", humanize.Plural(left, "step"))
	}
	s.emit(GroupFinished{Index: gi, Result: gr})
}

// recap sums up a stage of groups, so the outcome can be read without
// following every column of the log.
func (s *sink) recap(base, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	groups := s.res.Groups[base : base+n]
	finished, failed := 0, 0
	for _, g := range groups {
		switch {
		case g.Finished():
			finished++
		case g.Failed >= 0:
			failed++
		}
	}
	line := fmt.Sprintf("--- %s: %d finished", humanize.Plural(n, s.plan.Noun()), finished)
	if failed > 0 {
		line += fmt.Sprintf(", %d failed", failed)
	}
	if rest := n - finished - failed; rest > 0 {
		line += fmt.Sprintf(", %d not finished", rest)
	}
	s.logf("", "%s", line)
	for _, g := range groups {
		if g.Failed >= 0 {
			s.logf("", "!!! %s: step %d: %v", oneLine(g.Name), g.Failed+1, g.Err)
		}
	}
}
