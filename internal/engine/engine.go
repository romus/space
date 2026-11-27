//go:build darwin

// Package engine is the only place in space that changes anything on disk.
// It runs the steps of a plan in order, and groups of steps side by side,
// streams their output, writes a log, and measures free space before and
// after.
package engine

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/romus/space/pkg/recipe"
)

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

	stop    atomic.Bool
	trashMu sync.Mutex // moves to the Trash happen one at a time
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
