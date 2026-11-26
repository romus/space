//go:build darwin

// Package engine is the only place in space that changes anything on disk.
// It runs the steps of a plan one at a time, streams their output, writes a
// log, and measures free space before and after.
package engine

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/romus/space/internal/humanize"
	"github.com/romus/space/internal/scan"
	"github.com/romus/space/pkg/recipe"
)

// Event is something the engine tells its caller while running.
type Event interface{}

// StepStarted is sent when a step begins.
type StepStarted struct{ Index int }

// StepOutput is one line printed by the running step.
type StepOutput struct {
	Index int
	Line  string
}

// StepFinished is sent when a step ends.
type StepFinished struct {
	Index  int
	Result StepResult
}

// NeedTerminal asks the caller to run a command that needs the real
// terminal, such as sudo asking for a password. Send the outcome on Done.
type NeedTerminal struct {
	Cmd  *exec.Cmd
	Done chan error
}

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

// Executor runs the command of a step.
type Executor interface {
	Exec(ctx context.Context, cmd []string, out io.Writer) error
}

// Engine runs plans.
type Engine struct {
	Home    string   // home directory, a floor for deletions
	Volume  string   // any path on the volume whose free space is measured
	LogPath string   // appended to; empty disables the log
	Exec    Executor // runs commands; nil uses the real one
	Trash   Trasher  // moves to the Trash; nil picks the system one
	// Measure reads the recipe's own number, like the size of a VM disk.
	Measure func(ctx context.Context) (label string, bytes int64, ok bool)
	// Interactive runs a command that needs the terminal. Nil runs it with
	// the process's own stdin and stdout.
	Interactive func(cmd *exec.Cmd) error

	stop atomic.Bool
}

// StopAfterStep asks the engine to stop once the current step is finished.
// Nothing is interrupted halfway.
func (e *Engine) StopAfterStep() { e.stop.Store(true) }

// Stopping reports whether a stop was requested.
func (e *Engine) Stopping() bool { return e.stop.Load() }

// Prepare records the identity of every path a plan will remove, so the
// engine can refuse a path that was replaced after the plan was shown.
func Prepare(p *recipe.Plan) {
	for i := range p.Steps {
		r := p.Steps[i].Remove
		if r == nil {
			continue
		}
		r.Inodes = make([]uint64, len(r.Paths))
		for j, path := range r.Paths {
			var st syscall.Stat_t
			if syscall.Lstat(path, &st) == nil {
				r.Inodes[j] = st.Ino
			}
		}
	}
}

// Run executes the plan and reports progress through emit.
func (e *Engine) Run(ctx context.Context, plan recipe.Plan, emit func(Event)) Result {
	if emit == nil {
		emit = func(Event) {}
	}
	exe := e.Exec
	if exe == nil {
		exe = realExec{}
	}
	res := Result{Plan: plan, Steps: make([]StepResult, len(plan.Steps)), Failed: -1, LogPath: e.LogPath}
	log := e.openLog()
	defer log.Close()

	start := time.Now()
	fmt.Fprintf(log, "\n=== %s  %s — %s\n", start.Format("2006-01-02 15:04:05"), plan.Title, humanize.Plural(len(plan.Steps), "step"))

	res.FreeBefore = scan.FreeBytes(e.Volume)
	if e.Measure != nil {
		if label, n, ok := e.Measure(ctx); ok {
			res.Metric = &recipe.Metric{Label: label, Before: n, After: n}
		}
	}

	for i, step := range plan.Steps {
		if e.stop.Load() || ctx.Err() != nil {
			res.Stopped = true
			fmt.Fprintf(log, "--- stopped before step %d\n", i+1)
			break
		}
		emit(StepStarted{Index: i})
		fmt.Fprintf(log, "--- step %d: %s\n$ %s\n", i+1, step.Title, Display(step, ""))
		out := &lineWriter{emit: func(line string) {
			fmt.Fprintln(log, line)
			emit(StepOutput{Index: i, Line: line})
		}}
		t0 := time.Now()
		var (
			err      error
			bytes    int64
			failures []Failure
		)
		switch {
		case step.Remove != nil:
			bytes, failures, err = e.remove(ctx, step.Remove, out)
			if step.Remove.ToTrash {
				res.Trashed += bytes
			}
		case len(step.Cmd) == 0:
			err = errors.New("step has no command")
		case step.Sudo:
			err = e.interactive(ctx, append([]string{"sudo"}, step.Cmd...), emit)
		default:
			err = exe.Exec(ctx, step.Cmd, out)
		}
		out.Flush()
		sr := StepResult{Ran: true, Err: err, Elapsed: time.Since(t0), Bytes: bytes, Failures: failures}
		if err != nil {
			sr.Tail = out.Tail()
		}
		res.Steps[i] = sr
		if err != nil {
			fmt.Fprintf(log, "!!! step %d failed: %v\n", i+1, err)
		}
		emit(StepFinished{Index: i, Result: sr})
		if err != nil && ctx.Err() != nil {
			// Cut short by the user, not broken.
			res.Stopped = true
			break
		}
		if err != nil {
			res.Failed = i
			res.Err = err
			break
		}
	}

	res.FreeAfter = scan.FreeBytes(e.Volume)
	if res.Metric != nil {
		if _, n, ok := e.Measure(ctx); ok {
			res.Metric.After = n
		}
	}
	res.Elapsed = time.Since(start)
	fmt.Fprintf(log, "=== done in %s: free %s → %s\n", humanize.Duration(res.Elapsed),
		humanize.Bytes(res.FreeBefore), humanize.Bytes(res.FreeAfter))
	return res
}

func (e *Engine) interactive(ctx context.Context, argv []string, emit func(Event)) error {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if e.Interactive != nil {
		return e.Interactive(cmd)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func (e *Engine) openLog() io.WriteCloser {
	if e.LogPath == "" {
		return nopCloser{io.Discard}
	}
	if err := os.MkdirAll(filepath.Dir(e.LogPath), 0o755); err != nil {
		return nopCloser{io.Discard}
	}
	f, err := os.OpenFile(e.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nopCloser{io.Discard}
	}
	return f
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

// --- running commands -------------------------------------------------------

type realExec struct{}

func (realExec) Exec(ctx context.Context, argv []string, out io.Writer) error {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.Stdin = nil
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return fmt.Errorf("%s exited with status %d", argv[0], ee.ExitCode())
		}
		return err
	}
	return nil
}

// lineWriter splits a stream into lines and hands each to emit.
type lineWriter struct {
	mu   sync.Mutex
	buf  []byte
	last []string // the last few lines, for the result of a failed step
	emit func(string)
}

func (w *lineWriter) line(s string) {
	w.last = append(w.last, s)
	if len(w.last) > tailLines {
		w.last = w.last[1:]
	}
	w.emit(s)
}

// Tail returns the last lines written so far.
func (w *lineWriter) Tail() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.last...)
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := strings.IndexAny(string(w.buf), "\n\r")
		if i < 0 {
			break
		}
		line := string(w.buf[:i])
		w.buf = w.buf[i+1:]
		if line != "" {
			w.line(line)
		}
	}
	return len(p), nil
}

func (w *lineWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		w.line(string(w.buf))
		w.buf = nil
	}
}

// --- removing files ---------------------------------------------------------

// remove deletes or trashes the paths of a step, one by one, refusing
// anything outside the step's root.
//
// The paths are independent, so one that cannot be removed does not stop
// the rest: it is reported as a Failure with the reason. Only a root that
// is not allowed at all fails the whole step.
func (e *Engine) remove(ctx context.Context, r *recipe.Remove, out io.Writer) (int64, []Failure, error) {
	root, err := CheckRoot(r.Root, e.Home)
	if err != nil {
		return 0, nil, err
	}
	trash := e.Trash
	if r.ToTrash && trash == nil {
		trash = SystemTrash(e.Home)
	}
	var (
		total    int64
		failures []Failure
	)
	for i, p := range r.Paths {
		if ctx.Err() != nil {
			return total, failures, ctx.Err()
		}
		var ino uint64
		if i < len(r.Inodes) {
			ino = r.Inodes[i]
		}
		size, err := e.removeOne(p, root, ino, r.ToTrash, trash)
		switch {
		case errors.Is(err, os.ErrNotExist):
			fmt.Fprintf(out, "already gone: %s\n", humanize.Tilde(p, e.Home))
		case err != nil:
			f := Failure{Path: p, Reason: Explain(err), Err: err}
			failures = append(failures, f)
			fmt.Fprintf(out, "could not remove %s: %s\n", humanize.Tilde(p, e.Home), f.Reason)
		default:
			total += size
			verb := "removed"
			if r.ToTrash {
				verb = "trashed"
			}
			fmt.Fprintf(out, "%s %s  %s\n", verb, humanize.Bytes(size), humanize.Tilde(p, e.Home))
		}
	}
	return total, failures, nil
}

// Explain says in plain words why a path could not be removed.
func Explain(err error) string {
	switch {
	case errors.Is(err, ErrUnsafe), errors.Is(err, ErrChanged):
		return err.Error()
	case errors.Is(err, fs.ErrPermission):
		return "macOS did not allow it. Give your terminal Full Disk Access in " +
			"System Settings › Privacy & Security, or remove it in Finder"
	case errors.Is(err, syscall.EBUSY):
		return "in use by another program"
	case errors.Is(err, syscall.EROFS):
		return "the disk is read-only"
	}
	// The system's own words, without the path repeated in front.
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err.Error()
	}
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		return linkErr.Err.Error()
	}
	return err.Error()
}

// ErrUnsafe is returned when a path fails the safety checks.
var ErrUnsafe = errors.New("refused: outside the allowed folder")

// ErrChanged is returned when a path is not the one the plan was made for.
var ErrChanged = errors.New("refused: changed since the plan was made")

// CheckRoot decides whether files may be removed under root, and returns
// the root with symlinks resolved. Nothing may be removed under "/" or under
// any folder that contains the home directory.
func CheckRoot(root, home string) (string, error) {
	if root == "" || !filepath.IsAbs(root) {
		return "", fmt.Errorf("%w: no root", ErrUnsafe)
	}
	root = filepath.Clean(root)
	if root == "/" {
		return "", fmt.Errorf("%w: root is /", ErrUnsafe)
	}
	// The root may be the home directory, but never anything above it.
	if home != "" && root != home && strings.HasPrefix(home+"/", root+"/") {
		return "", fmt.Errorf("%w: root is above the home directory", ErrUnsafe)
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	return root, nil
}

func (e *Engine) removeOne(path, root string, ino uint64, toTrash bool, trash Trasher) (int64, error) {
	if !filepath.IsAbs(path) {
		return 0, ErrUnsafe
	}
	path = filepath.Clean(path)
	// Resolve the parent, not the path itself: a symlink is removed as a
	// link, but it must not be reached through a link that leaves the root.
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return 0, err
	}
	real := filepath.Join(parent, filepath.Base(path))
	if real == root || !strings.HasPrefix(real, root+string(filepath.Separator)) {
		return 0, ErrUnsafe
	}
	if real == "/" || real == e.Home || e.Home != "" && strings.HasPrefix(e.Home+"/", real+"/") {
		return 0, ErrUnsafe
	}
	var st syscall.Stat_t
	if err := syscall.Lstat(real, &st); err != nil {
		return 0, err
	}
	if ino != 0 && st.Ino != ino {
		return 0, ErrChanged
	}
	size := scan.SizeOf(real)
	if toTrash {
		return size, trash.Trash(real)
	}
	return size, os.RemoveAll(real)
}

// --- output helpers ---------------------------------------------------------

// ReadTail returns the last n lines of a file, for showing a log.
func ReadTail(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	return lines
}
