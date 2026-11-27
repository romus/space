//go:build darwin

package engine

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/romus/space/pkg/recipe"
)

// step runs one step and reports it. It is the only place a step is
// executed, whether on its own or inside a group.
func (r *run) step(ctx context.Context, i int) StepResult {
	s := r.plan.Steps[i]
	r.sink.started(i)
	out := &lineWriter{emit: func(line string) { r.sink.output(i, line) }}
	t0 := time.Now()
	bytes, failures, err := r.execute(ctx, s, out)
	out.Flush()
	sr := StepResult{Ran: true, Err: err, Elapsed: time.Since(t0), Bytes: bytes, Failures: failures}
	if err != nil {
		sr.Tail = out.Tail()
	}
	var trashed int64
	if s.Remove != nil && s.Remove.ToTrash {
		trashed = bytes
	}
	r.sink.finished(i, sr, trashed)
	return sr
}

func (r *run) execute(ctx context.Context, s recipe.Step, out io.Writer) (int64, []Failure, error) {
	switch {
	case s.Remove != nil:
		return r.e.remove(ctx, s.Remove, out)
	case len(s.Cmd) == 0:
		return 0, nil, errors.New("step has no command")
	case s.Sudo:
		return 0, nil, r.e.interactive(ctx, append([]string{"sudo"}, s.Cmd...))
	}
	return 0, nil, r.exe.Exec(ctx, s.Cmd, out)
}
