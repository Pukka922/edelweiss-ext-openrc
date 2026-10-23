// Package actions turns a request of the UI ("stop sshd") into a checked plan: the steps of a job (internal/jobs). Build
// checks the request against a fresh snapshot of the services and asks the policy, and it is the last check before a
// command touches a service. The commands are built from the name of the snapshot, with openrc.Tools, never from the
// text of the request.
package actions

import (
	"context"
	"fmt"
	"slices"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/host"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/jobs"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/openrc"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/policy"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/run"
)

// The codes of a refusal; internal/api maps them to HTTP statuses, the UI to texts.
const (
	CodeNoOpenRC  = "no_openrc"
	CodeInvalid   = "invalid_input"
	CodeNotFound  = "not_found"
	CodeProtected = "protected"
	CodeConfirm   = "confirm_required"
	CodeFailed    = "failed"
)

// Request is a change the UI asks for. Runlevel is for enable (default, or boot; empty means default) and disable (the
// runlevel to leave; empty means every one of default and boot the service is in), and for no other kind.
type Request struct {
	Kind     string `json:"kind"` // start, stop, restart, enable, disable
	Service  string `json:"service"`
	Runlevel string `json:"runlevel,omitempty"`
	Confirm  bool   `json:"confirm,omitempty"` // the red warning of a risky change was confirmed
}

// Env is what a plan needs of the host: the mode (diskless and data save /etc with lbu) and the tools.
type Env struct {
	Mode  host.Mode
	Tools openrc.Tools
}

// Plan is a request as the steps of a job. Kind is what the plan does: the kind of the request, except that the start
// of a crashed service is a restart.
type Plan struct {
	Kind, Target string
	Steps        []jobs.Step
}

// Error is a request the planner refuses. Field names the input of an invalid_input (kind, service or runlevel). A
// protected refusal names the Service that is protected, the target or a dependent that stops along, and its Reason (the
// protection class). A confirm_required one has the Reason of the warning and the running Services that stop along.
type Error struct {
	Code, Message, Field, Reason, Service string
	Services                              []string
}

func (e *Error) Error() string { return e.Message }

func invalid(field, format string, a ...any) *Error {
	return &Error{Code: CodeInvalid, Field: field, Message: fmt.Sprintf(format, a...)}
}

func refuse(code, format string, a ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, a...)}
}

// labelLbu is the label of the lbu step: the UI shows steps.<label> while a step runs. The labels of the commands of a
// service and of the autostart are the actions.
const labelLbu = "lbu"

// kinds are the requests Build knows.
var kinds = []policy.Action{policy.Start, policy.Stop, policy.Restart, policy.Enable, policy.Disable}

// modes are the system modes Build knows: whether a change of the autostart is saved with lbu depends on it.
var modes = []host.Mode{host.Sys, host.Diskless, host.Data}

// runlevels are the runlevels of the autostart the extension switches, in the order disable leaves them.
var runlevels = []string{"default", "boot"}

// Build plans the request r on the services of snap. The checks come in this order, each with its own refusal:
// OpenRC is there (no_openrc), the kind, the name of the service, the service is in snap (not_found), the runlevel,
// the change is not already done (invalid_input), the services that stop along (deps names those that need the
// service; failed when it fails or names one that snap does not hold), the policy: protected, or confirm_required
// unless r.Confirm. Last, an enable or disable needs a known env.Mode (sys, diskless or data), else it is failed: it
// would not know whether to save the change with lbu. Start, stop and restart ignore the mode.
// deps is asked for stop and restart only, and for the start of a crashed service, which is planned as a restart:
// OpenRC's start does nothing to a service it keeps in its started state.
func Build(ctx context.Context, snap openrc.Snapshot, deps func(ctx context.Context, name string) ([]string, error),
	r Request, env Env) (Plan, error) {
	if !snap.Present {
		return Plan{}, refuse(CodeNoOpenRC, "OpenRC is not running on this system")
	}
	a := policy.Action(r.Kind)
	if !slices.Contains(kinds, a) {
		return Plan{}, invalid("kind", "%q is not a kind of change", r.Kind)
	}
	if !openrc.ValidName(r.Service) {
		return Plan{}, invalid("service", "%q is not the name of a service", r.Service)
	}
	target, ok := find(snap, r.Service)
	if !ok {
		return Plan{}, refuse(CodeNotFound, "there is no service %s", r.Service)
	}
	if err := checkRunlevel(a, r.Runlevel); err != nil {
		return Plan{}, err
	}
	a, levels, err := settle(target, a, r.Runlevel)
	if err != nil {
		return Plan{}, err
	}
	var needing []openrc.Service
	if a == policy.Stop || a == policy.Restart {
		if needing, err = resolve(ctx, snap, deps, target.Name); err != nil {
			return Plan{}, err
		}
	}
	switch d := policy.Decide(target, a, needing); d.Verdict {
	case policy.Allow:
	case policy.Warn:
		if !r.Confirm {
			return Plan{}, &Error{Code: CodeConfirm, Reason: d.Reason, Services: d.Along,
				Message: fmt.Sprintf("%s of %s needs a confirmation (%s)", a, target.Name, d.Reason)}
		}
	default: // deny, and whatever verdict the policy may get later
		return Plan{}, denial(target.Name, a, d)
	}
	if (a == policy.Enable || a == policy.Disable) && !slices.Contains(modes, env.Mode) {
		return Plan{}, refuse(CodeFailed, "the system mode %q is neither sys, diskless nor data", env.Mode)
	}
	return Plan{Kind: string(a), Target: target.Name, Steps: steps(a, target.Name, levels, env)}, nil
}

// find is the service of snap of that name.
func find(snap openrc.Snapshot, name string) (openrc.Service, bool) {
	i := slices.IndexFunc(snap.Services, func(s openrc.Service) bool { return s.Name == name })
	if i < 0 {
		return openrc.Service{}, false
	}
	return snap.Services[i], true
}

// checkRunlevel checks the runlevel of the request: enable and disable take default or boot (or none), the other kinds
// take none.
func checkRunlevel(a policy.Action, runlevel string) error {
	switch {
	case a == policy.Enable || a == policy.Disable:
		if runlevel != "" && !slices.Contains(runlevels, runlevel) {
			return invalid("runlevel", "%q is not a runlevel of the autostart (default or boot)", runlevel)
		}
	case runlevel != "":
		return invalid("runlevel", "%s takes no runlevel", a)
	}
	return nil
}

// settle checks that the change is not done already and says what to do: the action, which for the start of a crashed
// service is a restart, and the runlevels of enable and disable.
func settle(s openrc.Service, a policy.Action, runlevel string) (policy.Action, []string, error) {
	switch a {
	case policy.Start:
		switch s.State {
		case openrc.Started, openrc.Starting:
			return a, nil, invalid("kind", "%s is already %s", s.Name, s.State)
		case openrc.Crashed:
			return policy.Restart, nil, nil
		}
	case policy.Stop:
		if s.State == openrc.Stopped || s.State == openrc.Stopping {
			return a, nil, invalid("kind", "%s is already %s", s.Name, s.State)
		}
	case policy.Enable:
		if runlevel == "" {
			runlevel = runlevels[0]
		}
		if slices.Contains(s.Runlevels, runlevel) {
			return a, nil, invalid("kind", "%s is already in the runlevel %s", s.Name, runlevel)
		}
		return a, []string{runlevel}, nil
	case policy.Disable:
		var levels []string
		for _, l := range runlevels {
			if (runlevel == "" || runlevel == l) && slices.Contains(s.Runlevels, l) {
				levels = append(levels, l)
			}
		}
		switch {
		case len(levels) > 0:
			return a, levels, nil
		case runlevel != "":
			return a, nil, invalid("runlevel", "%s is not in the runlevel %s", s.Name, runlevel)
		}
		return a, nil, invalid("kind", "%s is in neither default nor boot", s.Name)
	}
	return a, nil, nil
}

// resolve are the services of snap that need the named service, directly or through others. A lookup that fails, or
// a name that snap does not hold (the list changed since it was read), is a failure: a plan never leaves out a
// service that stops along.
func resolve(ctx context.Context, snap openrc.Snapshot, deps func(ctx context.Context, name string) ([]string, error),
	name string) ([]openrc.Service, error) {
	names, err := deps(ctx, name)
	if err != nil {
		return nil, refuse(CodeFailed, "the services that need %s cannot be read: %v", name, err)
	}
	found := make([]openrc.Service, 0, len(names))
	for _, n := range names {
		s, ok := find(snap, n)
		if !ok {
			return nil, refuse(CodeFailed, "the service list changed: %s, which needs %s, is not in it", n, name)
		}
		found = append(found, s)
	}
	return found, nil
}

// denial is the refusal of a denied action: the protected service is the dependent that blocks it, or else the target.
func denial(target string, a policy.Action, d policy.Decision) *Error {
	if d.Blocker == "" {
		return &Error{Code: CodeProtected, Reason: d.Reason, Service: target,
			Message: fmt.Sprintf("%s of %s is not allowed (%s)", a, target, d.Reason)}
	}
	return &Error{Code: CodeProtected, Reason: d.Reason, Service: d.Blocker,
		Message: fmt.Sprintf("%s of %s would stop %s, which is protected (%s)", a, target, d.Blocker, d.Reason)}
}

// steps are the steps of the action. The steps that change what OpenRC starts at boot are followed by one lbu commit
// in diskless and data mode.
func steps(a policy.Action, name string, levels []string, env Env) []jobs.Step {
	t, label := env.Tools, string(a)
	var out []jobs.Step
	switch a {
	case policy.Start:
		out = append(out, newStep(label, t.Start(name), name, jobs.MarkStart))
	case policy.Restart:
		out = append(out, newStep(label, t.Restart(name), name, jobs.MarkStart))
	case policy.Stop:
		out = append(out, newStep(label, t.Stop(name), name, jobs.MarkStop))
	case policy.Enable:
		for _, l := range levels {
			out = append(out, newStep(label, t.Enable(name, l), "", ""))
		}
	case policy.Disable:
		for _, l := range levels {
			out = append(out, newStep(label, t.Disable(name, l), "", ""))
		}
	}
	if (a == policy.Enable || a == policy.Disable) && (env.Mode == host.Diskless || env.Mode == host.Data) {
		out = append(out, newStep(labelLbu, t.LbuCommit(), "", ""))
	}
	return out
}

// newStep is the step that runs c; service and mark tell the runner what the outcome means for a service.
func newStep(label string, c run.Cmd, service, mark string) jobs.Step {
	return jobs.Step{Op: jobs.OpExec, Label: label, Cmd: &c, Service: service, Mark: mark}
}
