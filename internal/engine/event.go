//go:build darwin

package engine

import "os/exec"

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

// GroupFinished is sent when a group of steps ends: finished, failed, or
// left unfinished by a stop. Index points into Result.Groups.
type GroupFinished struct {
	Index  int
	Result GroupResult
}

// NeedTerminal asks the caller to run a command that needs the real
// terminal, such as sudo asking for a password. Send the outcome on Done.
type NeedTerminal struct {
	Cmd  *exec.Cmd
	Done chan error
}
