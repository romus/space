//go:build darwin

package screens

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/romus/space/internal/engine"
	"github.com/romus/space/internal/humanize"
	"github.com/romus/space/internal/ui/kit"
)

func secs(n int) time.Duration { return time.Duration(n) * time.Second }

const (
	stepWait = iota
	stepRun
	stepDone
	stepFailed
)

type runEventMsg struct{ ev any }

// Run shows a plan being executed, step by step.
type Run struct {
	c      *Ctx
	spec   PlanSpec
	eng    *engine.Engine
	events chan any

	status  []int
	results []engine.StepResult
	cur     int
	tail    []string
	start   time.Time

	metricLabel  string
	metricBefore int64
	metricNow    int64
	stopping     bool
	cancel       context.CancelFunc
	ticks        int
}

// runCmd starts the engine and switches to the run screen.
func runCmd(c *Ctx, spec PlanSpec) tea.Cmd {
	r := &Run{
		c: c, spec: spec,
		eng:     c.NewEngine(spec.Measure),
		events:  make(chan any, 256),
		status:  make([]int, len(spec.Plan.Steps)),
		results: make([]engine.StepResult, len(spec.Plan.Steps)),
		start:   time.Now(),
	}
	if spec.Measure != nil {
		if label, n, ok := spec.Measure(context.Background()); ok {
			r.metricLabel, r.metricBefore, r.metricNow = label, n, n
		}
	}
	// A step that needs the terminal (sudo asking for a password) is handed
	// to the app, which suspends the screen while it runs.
	r.eng.Interactive = func(cmd *exec.Cmd) error {
		done := make(chan error, 1)
		r.events <- engine.NeedTerminal{Cmd: cmd, Done: done}
		return <-done
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	go func() {
		res := r.eng.Run(ctx, spec.Plan, func(ev engine.Event) { r.events <- ev })
		r.events <- res
		close(r.events)
	}()
	// Only the screen itself reads the events, and only once it is on the
	// stack (see Start). Reading here as well would race with the switch of
	// screens: a fast step could report to a screen that is not there yet,
	// and the run would look stuck with the work already done.
	return replace(r)
}

// Start begins reading what the engine reports.
func (r *Run) Start() tea.Cmd { return r.wait() }

func (r *Run) wait() tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-r.events
		if !ok {
			return nil
		}
		return runEventMsg{ev}
	}
}

func (r *Run) Title() string { return strings.ToLower(r.spec.Plan.Title) + " — running" }
func (r *Run) Typing() bool  { return false }

// Interrupt turns ctrl+c into "stop after this step": nothing is cut off
// halfway. A second ctrl+c is for a step that will not end: it stops the
// running command at once.
func (r *Run) Interrupt() (Screen, tea.Cmd) {
	if r.stopping && r.cancel != nil {
		r.cancel()
	}
	r.stopping = true
	r.eng.StopAfterStep()
	return r, nil
}

func (r *Run) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case TickMsg:
		r.ticks++
		if r.spec.Measure != nil && r.ticks%10 == 0 {
			if _, n, ok := r.spec.Measure(context.Background()); ok {
				r.metricNow = n
			}
		}
	case runEventMsg:
		switch ev := msg.ev.(type) {
		case engine.StepStarted:
			r.cur = ev.Index
			r.status[ev.Index] = stepRun
			r.tail = nil
		case engine.StepOutput:
			r.tail = append(r.tail, ev.Line)
			if len(r.tail) > 200 {
				r.tail = r.tail[len(r.tail)-200:]
			}
		case engine.StepFinished:
			r.results[ev.Index] = ev.Result
			r.status[ev.Index] = stepDone
			if ev.Result.Err != nil {
				r.status[ev.Index] = stepFailed
			}
		case engine.NeedTerminal:
			return r, tea.Batch(tea.ExecProcess(ev.Cmd, func(err error) tea.Msg {
				ev.Done <- err
				return nil
			}), r.wait())
		case engine.Result:
			return r, replace(NewDone(r.c, r.spec, ev))
		}
		return r, r.wait()
	case tea.KeyPressMsg:
		if keyOf(msg) == "l" {
			return r, openLog(r.eng.LogPath)
		}
	}
	return r, nil
}

func (r *Run) Lines() []string {
	c, t := r.c, r.c.Theme
	w := c.W
	steps := r.spec.Plan.Steps
	muted := t.S(t.Muted)

	top := []string{
		header(c, r.spec.crumbs(c, "Running"), []kit.Span{kit.Sp(muted, humanize.Clock(time.Since(r.start)))}),
		kit.Blank(t, w),
	}

	// Progress: finished steps, plus half a cell for the one in flight.
	const cols = 26
	done := 0
	for _, s := range r.status {
		if s == stepDone || s == stepFailed {
			done++
		}
	}
	filled := 0
	if len(steps) > 0 {
		filled = done * cols / len(steps)
	}
	bar := strings.Repeat("█", filled)
	if filled < cols {
		bar += "▌" + strings.Repeat("░", cols-filled-1)
	}
	top = append(top,
		kit.Row(w, t.Base(), []kit.Span{
			kit.Sp(t.S(t.Strong), bar),
			kit.Sp(t.S(t.Strong), fmt.Sprintf("  step %d of %d", min(r.cur+1, len(steps)), len(steps))),
		}, nil),
		kit.Blank(t, w))

	titleW := min(44, max(20, w/3))
	for i, s := range steps {
		glyph, gc, nc := "·", t.Faint, t.Muted
		st := t.S
		res := ""
		switch r.status[i] {
		case stepDone:
			glyph, gc, nc = "✓", t.Safe, t.Strong
			res = humanize.Duration(r.results[i].Elapsed)
			if b := r.results[i].Bytes; b > 0 {
				res = humanize.Bytes(b) + " · " + res
			}
			if n := len(r.results[i].Failures); n > 0 {
				glyph, gc = "!", t.Danger
				res = fmt.Sprintf("%d failed", n)
			}
		case stepFailed:
			glyph, gc, nc = "✗", t.Danger, t.Strong
			res = "failed"
		case stepRun:
			glyph, gc, nc = kit.Spinner(c.Frame), t.Strong, t.Strong
			st = t.Sel
		}
		top = append(top, kit.Row(w, st(t.Text), []kit.Span{
			kit.Cell(st(t.Text), "", 1), kit.Cell(st(gc), glyph, 3),
			kit.Cell(st(nc), s.Title, titleW), kit.Cell(st(t.Text), "", 2),
			kit.Sp(st(t.Faint), kit.Fit(engine.Display(s, c.Home()), max(0, w-titleW-6-14))),
		}, []kit.Span{kit.CellR(st(t.Muted), res, 13), kit.Cell(st(t.Text), "", 1)}))
	}

	// The last lines the running command printed.
	if r.cur < len(steps) {
		const show = 4
		lines := []string{kit.Row(w-4, t.Base(), []kit.Span{kit.Sp(t.S(t.Faint), engine.Display(steps[r.cur], c.Home()))}, nil)}
		tail := r.tail
		if len(tail) > show {
			tail = tail[len(tail)-show:]
		}
		for i, l := range tail {
			st := muted
			if i == len(tail)-1 {
				st = t.S(t.Subtle)
			}
			lines = append(lines, kit.Row(w-4, t.Base(), []kit.Span{kit.Sp(st, strings.ReplaceAll(l, "\t", "  "))}, nil))
		}
		top = append(top, kit.Blank(t, w))
		top = append(top, kit.Box(t, w, lines)...)
	}

	bottom := []string{kit.Rule(t, w)}
	if r.metricLabel != "" {
		spans := []kit.Span{
			kit.Sp(muted, r.metricLabel+"   "), kit.Sp(t.S(t.Text), humanize.Bytes(r.metricBefore)),
			kit.Sp(t.S(t.Faint), "   →   "), kit.Sp(t.S(t.Strong).Bold(true), humanize.Bytes(r.metricNow)),
		}
		if r.metricNow < r.metricBefore {
			spans = append(spans, kit.Sp(muted, "   and shrinking"))
		}
		bottom = append(bottom, kit.Row(w, t.Base(), spans, nil))
	} else {
		bottom = append(bottom, kit.Blank(t, w))
	}
	stop := "stop after this step"
	if r.stopping {
		stop = "stopping after this step… again to cut it short"
	}
	bottom = append(bottom, kit.Blank(t, w), kit.Footer(t, w,
		[]kit.Hint{{"l", "full log"}, {"ctrl+c", stop}},
		[]kit.Span{kit.Sp(muted, "log: "+c.Tilde(r.eng.LogPath))}, "", ""))
	return kit.Frame(t, w, c.H, top, bottom)
}
