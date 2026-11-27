//go:build darwin

package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Executor runs the command of a step.
type Executor interface {
	Exec(ctx context.Context, cmd []string, out io.Writer) error
}

func (e *Engine) interactive(ctx context.Context, argv []string) error {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if e.Interactive != nil {
		return e.Interactive(cmd)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

type realExec struct{}

func (realExec) Exec(ctx context.Context, argv []string, out io.Writer) error {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.Stdin = nil
	// A cancelled command is asked to end, and killed only if it does not:
	// git killed outright leaves its lock files behind.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
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
