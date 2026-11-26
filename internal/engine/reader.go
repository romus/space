//go:build darwin

package engine

import (
	"context"
	"os/exec"
	"time"
)

// Reader runs the read-only commands recipes use to look around. It never
// runs anything from a plan: that is the engine's job.
type Reader struct{}

// Output runs a command and returns what it printed. It gives up after a
// while so a hung tool cannot freeze the screen.
func (Reader) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

// LookPath finds a command on PATH.
func (Reader) LookPath(name string) (string, error) { return exec.LookPath(name) }
