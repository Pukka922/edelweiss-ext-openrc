package jobs

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/openrc"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/run"
)

// Executor does the steps of a job. Root is the root of the system: it is prefixed to every file path (MarkDir), never
// to the paths in commands.
//
// OpenRC never marks a failed start, so a step with a Mark leaves markers for the view, one file per service in
// MarkDir/ok and MarkDir/failed. When the command of the step works, MarkDir/ok/<service> is written and
// MarkDir/failed/<service> removed; when the command of a start (MarkStart) fails, MarkDir/failed/<service> is written,
// also when the step is Ignore; a failed stop (MarkStop) writes nothing. A marker holds the time as RFC 3339 in UTC;
// ReadMarks reads them. A marker that cannot be written does not change the outcome of the step: Note, when set, gets
// the reason (RunJob puts it into the log of the job).
type Executor struct {
	Root    string
	Runner  run.Runner
	MarkDir string // below Root; DefaultMarkDir when empty
	Note    func(format string, args ...any)
}

// Do does the step. A failure is a *StepError.
func (x Executor) Do(ctx context.Context, s Step) error {
	if s.Op != OpExec {
		return &StepError{Code: codeFailed, Message: fmt.Sprintf("unknown step %q", s.Op)}
	}
	return x.exec(ctx, s)
}

// exec runs the step's command, then marks its outcome. A step whose Mark does not fit does not run its command.
func (x Executor) exec(ctx context.Context, s Step) error {
	if s.Cmd == nil {
		return &StepError{Code: codeFailed, Message: "exec: no command"}
	}
	if err := s.checkMark(); err != nil {
		return &StepError{Code: codeFailed, Message: Describe(s) + ": " + err.Error()}
	}
	out, err := x.Runner.Run(ctx, *s.Cmd)
	x.mark(s, err == nil)
	if err == nil || s.Ignore {
		return nil
	}
	return commandError(Describe(s), *s.Cmd, out, err)
}

// checkMark is an error unless the mark of the step is none, or MarkStart or MarkStop for the name of a service: the
// name becomes a file name.
func (s Step) checkMark() error {
	switch {
	case s.Mark == "":
		return nil
	case s.Mark != MarkStart && s.Mark != MarkStop:
		return fmt.Errorf("unknown mark %q", s.Mark)
	case !openrc.ValidName(s.Service):
		return fmt.Errorf("mark %s: %q is no service name", s.Mark, s.Service)
	}
	return nil
}

// commandError is the StepError of the command c that printed out and failed with err; what names the step. The code
// is the one c.Codes gives the exit status, else failed.
func commandError(what string, c run.Cmd, out string, err error) *StepError {
	var re *run.Error
	if !errors.As(err, &re) {
		return &StepError{Code: codeFailed, Message: what + ": " + err.Error(), Output: strings.TrimSpace(out)}
	}
	e := &StepError{Code: codeFailed, Output: re.Output}
	if code, ok := c.Codes[re.Status]; ok {
		e.Code = code
	}
	switch {
	case re.Status >= 0:
		e.Message = fmt.Sprintf("%s: exit %d", what, re.Status)
	case re.Err != nil:
		e.Message = what + ": " + re.Err.Error()
	default:
		e.Message = what + ": did not start"
	}
	return e
}
