//go:build darwin

package agents

import (
	"errors"
	"os"
	"syscall"
)

// isAlive checks a process without disturbing it: signal 0 only tests.
func isAlive(p *os.Process) bool {
	err := p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
