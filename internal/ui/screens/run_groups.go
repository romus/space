//go:build darwin

package screens

import (
	"time"

	"github.com/romus/space/internal/engine"
	"github.com/romus/space/pkg/recipe"
)

// stepStopped is a group that a stop left unfinished.
const stepStopped = stepFailed + 1

// tailKept is how many lines of output each row remembers.
const tailKept = 50

// runRow is one line of a run with groups: a step on its own, or a whole
// group with the step it is at.
type runRow struct {
	name     string
	group    int   // index into the plan's groups, or -1 for a step on its own
	steps    []int // indices into Plan.Steps
	done     int   // steps that ran without error
	cur      int   // the step running now or run last, or -1
	state    int
	err      error
	elapsed  time.Duration
	bytes    int64
	failures int
	tail     []string
	seq      int // when it last printed, for following the liveliest row
}

// groupRun is the state of a run with groups. Several groups run at once,
// so each row keeps its own output.
type groupRun struct {
	rows     []runRow
	at       map[int]int // step → row
	byGroup  []int       // group → row
	cur, off int
	follow   bool // the cursor follows a running row on its own
	seq      int
}

// newGroupRun lays the rows out. It is nil for a plan without groups.
func newGroupRun(p recipe.Plan) *groupRun {
	if len(engine.Groups(p)) == 0 {
		return nil
	}
	g := &groupRun{at: map[int]int{}, follow: true}
	for _, st := range engine.Layout(p) {
		if len(st.Groups) == 0 {
			g.at[st.Step] = len(g.rows)
			g.rows = append(g.rows, runRow{name: p.Steps[st.Step].Title, group: -1, steps: []int{st.Step}, cur: -1})
			continue
		}
		for _, gr := range st.Groups {
			for _, i := range gr.Steps {
				g.at[i] = len(g.rows)
			}
			g.byGroup = append(g.byGroup, len(g.rows))
			g.rows = append(g.rows, runRow{name: gr.Name, group: len(g.byGroup) - 1, steps: gr.Steps, cur: -1})
		}
	}
	return g
}

// feed takes one event from the engine.
func (g *groupRun) feed(ev any) {
	switch ev := ev.(type) {
	case engine.StepStarted:
		row := &g.rows[g.at[ev.Index]]
		row.state, row.cur, row.tail = stepRun, ev.Index, nil
	case engine.StepOutput:
		row := &g.rows[g.at[ev.Index]]
		row.tail = append(row.tail, ev.Line)
		if len(row.tail) > tailKept {
			row.tail = row.tail[len(row.tail)-tailKept:]
		}
		g.seq++
		row.seq = g.seq
	case engine.StepFinished:
		row := &g.rows[g.at[ev.Index]]
		if row.group >= 0 {
			if ev.Result.Err == nil {
				row.done++
			}
			break // the group reports its own end
		}
		row.state, row.elapsed, row.err = stepDone, ev.Result.Elapsed, ev.Result.Err
		row.bytes, row.failures = ev.Result.Bytes, len(ev.Result.Failures)
		if ev.Result.Err != nil {
			row.state = stepFailed
		} else {
			row.done = 1
		}
	case engine.GroupFinished:
		row := &g.rows[g.byGroup[ev.Index]]
		row.done, row.elapsed, row.err = ev.Result.Done, ev.Result.Elapsed, ev.Result.Err
		switch {
		case ev.Result.Finished():
			row.state = stepDone
		case ev.Result.Failed >= 0:
			row.state, row.cur = stepFailed, ev.Result.Failed
		default:
			row.state = stepStopped
		}
	}
	if g.follow {
		g.pick()
	}
}

// pick moves the cursor to a running row, and leaves it there until that
// row ends: jumping between four busy rows would be unreadable.
func (g *groupRun) pick() {
	if g.rows[g.cur].state == stepRun {
		return
	}
	best := -1
	for i, row := range g.rows {
		if row.state == stepRun && (best < 0 || row.seq > g.rows[best].seq) {
			best = i
		}
	}
	if best >= 0 {
		g.cur = best
	}
}

// key lets the user look at a row of their choice; esc goes back to
// following.
func (g *groupRun) key(k string) {
	switch k {
	case "esc":
		g.follow = true
		g.pick()
	default:
		if move(&g.cur, k, len(g.rows), 10) {
			g.follow = false
		}
	}
}

// count says how many groups there are and how many of them ended in the
// given state.
func (g *groupRun) count(state int) (n, total int) {
	for _, row := range g.rows {
		if row.group < 0 {
			continue
		}
		total++
		if row.state == state {
			n++
		}
	}
	return n, total
}
