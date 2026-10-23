// Package jobs runs every change of the extension as a job. The API plans a job as a list of Steps and starts the
// extension's own binary with --run-job <dir> as a detached runner: in a session of its own, holding the job lock it
// inherits as fd 3. Stopping the extension, ending admin mode and restarting edelweiss itself therefore never stop an
// rc-service or an rc-update halfway. The runner writes the job's state and log into the job directory, which every
// process of the extension reads.
package jobs

import (
	"strings"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/run"
)

// OpExec is the operation of a Step that runs its command.
const OpExec = "exec"

// The marks of a Step: what its outcome means for the service it names. OpenRC never marks a failed start, so the
// runner does (see Executor).
const (
	MarkStart = "start" // the step starts the service: start and restart
	MarkStop  = "stop"  // the step stops the service
)

// Step is one step of a job. It is part of the job's spec.json.
type Step struct {
	Op      string   `json:"op"`    // exec
	Label   string   `json:"label"` // UI text key below "steps.": start, stop, restart, enable, disable, lbu
	Cmd     *run.Cmd `json:"cmd,omitempty"`
	Ignore  bool     `json:"ignore,omitempty"`  // exec: a failure does not stop the job
	Service string   `json:"service,omitempty"` // the service the Mark is about
	Mark    string   `json:"mark,omitempty"`    // MarkStart, MarkStop, or none
}

// Describe is the step as one line, for tests and the log: the op and its operand ("exec <command line>"). The service
// and the mark do not show: the line is the command.
func Describe(s Step) string {
	parts := []string{s.Op}
	if s.Op == OpExec && s.Cmd != nil {
		parts = append(parts, s.Cmd.String())
	}
	return strings.Join(parts, " ")
}

// The error codes of a job besides those of a command's Codes.
const (
	codeFailed      = "failed"
	codeInterrupted = "interrupted"
)

// StepError is a step that failed. Code is the error code of the job (failed, or the code a command's Codes give its
// exit status), Message says what failed, Output is what the failed command printed.
type StepError struct{ Code, Message, Output string }

func (e *StepError) Error() string { return e.Message }
