// Package run runs the programs of the extension: without a shell, by an absolute path with an argument list, with a
// fixed environment, "/" as the working directory and no stdin.
package run

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// Env is the whole environment of every program. EINFO_COLOR=NO keeps the colour codes out of what OpenRC prints.
var Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C", "EINFO_COLOR=NO"}

// DefaultTimeout limits a command whose Timeout is 0.
const DefaultTimeout = 30 * time.Second

// waitDelay is how long Run waits for a program to end after its timeout sent it SIGTERM, before it kills it.
const waitDelay = 30 * time.Second

// Cmd is one call of a program. It is part of a job's spec.json.
type Cmd struct {
	Path    string         `json:"path"`
	Args    []string       `json:"args"`
	Timeout time.Duration  `json:"timeout,omitempty"` // 0: DefaultTimeout; < 0: none
	OK      []int          `json:"ok,omitempty"`      // further exit statuses that are success
	Codes   map[int]string `json:"codes,omitempty"`   // exit status → error code of the job
}

// String is the command line: the path and the arguments, joined by spaces.
func (c Cmd) String() string { return strings.Join(append([]string{c.Path}, c.Args...), " ") }

// timeout is how long c may run; 0 is no limit.
func (c Cmd) timeout() time.Duration {
	switch {
	case c.Timeout == 0:
		return DefaultTimeout
	case c.Timeout < 0:
		return 0
	}
	return c.Timeout
}

// Runner runs commands.
type Runner interface {
	// Run runs c; the output is stdout and stderr together. It returns the output also when the command fails, with
	// an *Error when it ran and ended with a status that is not success.
	Run(ctx context.Context, c Cmd) (string, error)
}

// Error is a command that failed: it did not start, was killed, or ended with an exit status that is not success.
type Error struct {
	Path   string
	Args   []string
	Status int    // -1: did not start, was killed, or was ended by its timeout
	Output string // trimmed
	Err    error
}

func (e *Error) Error() string {
	msg := Cmd{Path: e.Path, Args: e.Args}.String()
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	if e.Output != "" {
		msg += ": " + e.Output
	}
	return msg
}

func (e *Error) Unwrap() error { return e.Err }

// failure is the result of a command that ended with status (-1: it did not start or was killed) and printed out:
// nil when the status is 0 or one of c.OK, else the *Error.
func failure(c Cmd, status int, out string, err error) error {
	if status == 0 || (status > 0 && slices.Contains(c.OK, status)) {
		return nil
	}
	return &Error{Path: c.Path, Args: c.Args, Status: status, Output: strings.TrimSpace(out), Err: err}
}

// Exec runs the programs on the host. Env is their whole environment (run.Env when nil).
type Exec struct{ Env []string }

// Run runs c with its timeout; the program has no stdin (the null device).
//
// The output goes to a temporary file (0600, removed afterwards), not to a pipe: a program that leaves a child behind
// which keeps the output open (a service that starts a daemon) neither keeps Run waiting nor makes it fail. Run reads
// what the file holds when the program has ended, and the status is the program's own.
//
// The program leads a process group of its own, which its children are in unless they leave it. When the timeout (or
// the end of ctx) comes first, Run sends SIGTERM to the whole group: rc-service is openrc-run by then (it execs the
// init script), which passes the signal on to the script and rolls a half-done start or stop back on its way out, so
// that the service is not left starting or stopping. Run waits for the program up to waitDelay, then kills it, and
// last sends SIGKILL to what is left of the group. A daemon that was started properly (start-stop-daemon --background,
// supervise-daemon) runs in a session of its own and is not touched. Such a command is a failure with Status -1 that
// wraps the error of ctx, whatever it exited with after SIGTERM.
func (x Exec) Run(ctx context.Context, c Cmd) (string, error) {
	if limit := c.timeout(); limit > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, limit)
		defer cancel()
	}
	f, err := os.CreateTemp("", "edelweiss-ext-openrc-run-*")
	if err != nil {
		return "", &Error{Path: c.Path, Args: c.Args, Status: -1, Err: fmt.Errorf("output file: %w", err)}
	}
	defer os.Remove(f.Name())
	defer f.Close()
	cmd := exec.CommandContext(ctx, c.Path, c.Args...)
	cmd.Env = x.Env
	if cmd.Env == nil {
		cmd.Env = Env // a nil Env would hand on the extension's own environment
	}
	cmd.Dir = "/"
	cmd.Stdout, cmd.Stderr = f, f // and a nil Stdin is the null device
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var terminated atomic.Bool // the timeout sent SIGTERM to the group
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone // the group has ended already: the status is the program's own
		}
		terminated.Store(err == nil)
		return err
	}
	cmd.WaitDelay = waitDelay
	err = cmd.Run()
	status := -1 // did not start, or was killed (ExitCode is -1 then too)
	if cmd.ProcessState != nil {
		status = cmd.ProcessState.ExitCode()
	}
	if terminated.Load() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) // what is left of the group; ESRCH when nothing is
		status = -1
		if !errors.Is(err, ctx.Err()) {
			err = fmt.Errorf("%w (%v)", ctx.Err(), err)
		}
	}
	b, readErr := os.ReadFile(f.Name())
	if readErr != nil {
		return "", &Error{Path: c.Path, Args: c.Args, Status: status, Err: fmt.Errorf("output file: %w", readErr)}
	}
	out := string(b)
	return out, failure(c, status, out, err)
}
