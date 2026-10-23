package actions

import (
	"cmp"
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/host"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/jobs"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/openrc"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/run"
)

var tools = openrc.Tools{RcService: "/sbin/rc-service", RcUpdate: "/sbin/rc-update", RcStatus: "/bin/rc-status", Lbu: "/usr/sbin/lbu"}

func svc(name string, state openrc.State, levels ...string) openrc.Service {
	return openrc.Service{Name: name, State: state, Runlevels: levels, Script: "/etc/init.d/" + name}
}

// snapshot is a present OpenRC with these services, sorted by name as Read leaves them.
func snapshot(services ...openrc.Service) openrc.Snapshot {
	slices.SortFunc(services, func(a, b openrc.Service) int { return cmp.Compare(a.Name, b.Name) })
	return openrc.Snapshot{Present: true, Services: services}
}

// world is a small system: sshd with a running and a stopped dependent, networking and a crashed net.eth0 that edelweiss
// needs, localmount that needs blkmon, services in every state of the runlevels default, boot, sysinit and nonetwork.
var world = snapshot(
	svc("edelweiss", openrc.Started, "default"),
	svc("networking", openrc.Started, "boot"),
	svc("net.eth0", openrc.Crashed, "default"),
	svc("localmount", openrc.Started, "boot"),
	svc("blkmon", openrc.Started, "default"),
	svc("sshd", openrc.Started, "default"),
	svc("dropbear", openrc.Crashed, "default"),
	svc("tunnel", openrc.Started, "default"),
	svc("oldtunnel", openrc.Stopped, "default"),
	svc("iptables", openrc.Stopped),
	svc("crond", openrc.Started, "default"),
	svc("chronyd", openrc.Crashed, "default"),
	svc("nginx", openrc.Stopped, "default"),
	svc("docker", openrc.Stopped),
	svc("root", openrc.Stopped),
	svc("both", openrc.Started, "boot", "default"),
	svc("bootonly", openrc.Started, "boot"),
	svc("mixed", openrc.Started, "default", "nonetwork"),
	svc("sysonly", openrc.Started, "sysinit"),
)

// needsme is what needs a service of world, as Reader.Dependents lists it.
var needsme = map[string][]string{
	"sshd":       {"tunnel", "oldtunnel"},
	"dropbear":   {"tunnel"},
	"networking": {"edelweiss"},
	"net.eth0":   {"edelweiss"},
	"blkmon":     {"localmount"},
}

// fakeDeps answers like Reader.Dependents and counts its calls.
type fakeDeps struct {
	needsme map[string][]string
	err     error
	calls   []string
	ctx     context.Context // the context of the last call
}

func newDeps() *fakeDeps { return &fakeDeps{needsme: needsme} }

func (f *fakeDeps) dependents(ctx context.Context, name string) ([]string, error) {
	f.calls, f.ctx = append(f.calls, name), ctx
	return f.needsme[name], f.err
}

func build(snap openrc.Snapshot, d *fakeDeps, r Request, mode host.Mode) (Plan, error) {
	return Build(context.Background(), snap, d.dependents, r, Env{Mode: mode, Tools: tools})
}

// lines are the steps of a plan as jobs.Describe prints them.
func lines(p Plan) []string {
	var out []string
	for _, s := range p.Steps {
		out = append(out, jobs.Describe(s))
	}
	return out
}

func bad(field string) Error { return Error{Code: CodeInvalid, Field: field} }
func fail(code string) Error { return Error{Code: code} }

// wantRefusal checks that err is an *Error of the shape want (its Message is only checked not to be empty) and returns it.
func wantRefusal(t *testing.T, err error, want Error) *Error {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("error = %v (%T), want an *Error %+v", err, err, want)
	}
	if e.Message == "" || e.Error() != e.Message {
		t.Errorf("Message %q, Error() %q: want the same text, not empty", e.Message, e.Error())
	}
	got := *e
	got.Message = ""
	if !reflect.DeepEqual(got, want) {
		t.Errorf("refusal\n got %+v\nwant %+v", got, want)
	}
	return e
}

// check builds r on world: it expects the steps, or the refusal when want has a code (and then no plan).
func check(t *testing.T, r Request, steps []string, want Error) {
	t.Helper()
	p, err := build(world, newDeps(), r, host.Sys)
	if want.Code != "" {
		wantRefusal(t, err, want)
		if !reflect.DeepEqual(p, Plan{}) {
			t.Errorf("%+v: a refusal comes with the plan %+v", r, p)
		}
		return
	}
	if err != nil {
		t.Fatalf("%+v: %v", r, err)
	}
	if got := lines(p); !slices.Equal(got, steps) {
		t.Errorf("%+v steps\n got %q\nwant %q", r, got, steps)
	}
}

func step(label string, c run.Cmd, service, mark string) jobs.Step {
	return jobs.Step{Op: jobs.OpExec, Label: label, Cmd: &c, Service: service, Mark: mark}
}

func TestPlans(t *testing.T) {
	for _, tc := range []struct {
		req   Request
		kind  string
		steps string
	}{
		{Request{Kind: "start", Service: "nginx"}, "start", "exec /sbin/rc-service nginx start"},
		{Request{Kind: "stop", Service: "crond"}, "stop", "exec /sbin/rc-service crond stop"},
		{Request{Kind: "stop", Service: "crond", Confirm: true}, "stop", "exec /sbin/rc-service crond stop"},
		{Request{Kind: "restart", Service: "crond"}, "restart", "exec /sbin/rc-service crond restart"},
		{Request{Kind: "restart", Service: "nginx"}, "restart", "exec /sbin/rc-service nginx restart"},
		{Request{Kind: "restart", Service: "edelweiss"}, "restart", "exec /sbin/rc-service edelweiss restart"},
		{Request{Kind: "enable", Service: "docker"}, "enable", "exec /sbin/rc-update add docker default"},
		{Request{Kind: "enable", Service: "docker", Runlevel: "default"}, "enable", "exec /sbin/rc-update add docker default"},
		{Request{Kind: "enable", Service: "docker", Runlevel: "boot"}, "enable", "exec /sbin/rc-update add docker boot"},
		{Request{Kind: "enable", Service: "crond", Runlevel: "boot"}, "enable", "exec /sbin/rc-update add crond boot"},
		{Request{Kind: "enable", Service: "bootonly"}, "enable", "exec /sbin/rc-update add bootonly default"},
		{Request{Kind: "disable", Service: "crond", Runlevel: "default"}, "disable", "exec /sbin/rc-update del crond default"},
		{Request{Kind: "disable", Service: "crond"}, "disable", "exec /sbin/rc-update del crond default"},
		{Request{Kind: "disable", Service: "bootonly"}, "disable", "exec /sbin/rc-update del bootonly boot"},
	} {
		p, err := build(world, newDeps(), tc.req, host.Sys)
		if err != nil {
			t.Errorf("%+v: %v", tc.req, err)
			continue
		}
		if p.Kind != tc.kind || p.Target != tc.req.Service || !slices.Equal(lines(p), []string{tc.steps}) {
			t.Errorf("%+v:\n got %s %s %q\nwant %s %s %q", tc.req, p.Kind, p.Target, lines(p), tc.kind, tc.req.Service, []string{tc.steps})
		}
	}
}

// TestStepFields: the commands are the Tools builders', the start and stop steps carry the service and the mark of
// what their outcome means, the autostart steps and the lbu step carry none.
func TestStepFields(t *testing.T) {
	for _, tc := range []struct {
		req  Request
		mode host.Mode
		want []jobs.Step
	}{
		{Request{Kind: "start", Service: "nginx"}, host.Sys,
			[]jobs.Step{step("start", tools.Start("nginx"), "nginx", jobs.MarkStart)}},
		{Request{Kind: "stop", Service: "crond"}, host.Sys,
			[]jobs.Step{step("stop", tools.Stop("crond"), "crond", jobs.MarkStop)}},
		{Request{Kind: "restart", Service: "crond"}, host.Sys,
			[]jobs.Step{step("restart", tools.Restart("crond"), "crond", jobs.MarkStart)}},
		{Request{Kind: "enable", Service: "docker", Runlevel: "boot"}, host.Sys,
			[]jobs.Step{step("enable", tools.Enable("docker", "boot"), "", "")}},
		{Request{Kind: "disable", Service: "both"}, host.Diskless, []jobs.Step{
			step("disable", tools.Disable("both", "default"), "", ""),
			step("disable", tools.Disable("both", "boot"), "", ""),
			step("lbu", tools.LbuCommit(), "", "")}},
	} {
		p, err := build(world, newDeps(), tc.req, tc.mode)
		if err != nil {
			t.Errorf("%+v: %v", tc.req, err)
		} else if !reflect.DeepEqual(p.Steps, tc.want) {
			t.Errorf("%+v steps\n got %+v\nwant %+v", tc.req, p.Steps, tc.want)
		}
	}
}

func TestWarnNeedsConfirm(t *testing.T) {
	for _, tc := range []struct {
		req   Request
		want  Error
		steps string
	}{
		// oldtunnel needs sshd too, but it is stopped: it does not stop along
		{Request{Kind: "stop", Service: "sshd"}, Error{Code: CodeConfirm, Reason: "remote", Services: []string{"tunnel"}},
			"exec /sbin/rc-service sshd stop"},
		{Request{Kind: "restart", Service: "sshd"}, Error{Code: CodeConfirm, Reason: "remote", Services: []string{"tunnel"}},
			"exec /sbin/rc-service sshd restart"},
		{Request{Kind: "disable", Service: "sshd"}, Error{Code: CodeConfirm, Reason: "remote"},
			"exec /sbin/rc-update del sshd default"},
		{Request{Kind: "start", Service: "iptables"}, Error{Code: CodeConfirm, Reason: "firewall"},
			"exec /sbin/rc-service iptables start"},
		{Request{Kind: "enable", Service: "iptables"}, Error{Code: CodeConfirm, Reason: "firewall"},
			"exec /sbin/rc-update add iptables default"},
	} {
		check(t, tc.req, nil, tc.want)
		tc.req.Confirm = true
		check(t, tc.req, []string{tc.steps}, Error{})
	}
}

func TestDenied(t *testing.T) {
	for _, tc := range []struct {
		req  Request
		want Error
	}{
		{Request{Kind: "stop", Service: "edelweiss"}, Error{Code: CodeProtected, Reason: "edelweiss", Service: "edelweiss"}},
		{Request{Kind: "disable", Service: "edelweiss"}, Error{Code: CodeProtected, Reason: "edelweiss", Service: "edelweiss"}},
		{Request{Kind: "restart", Service: "localmount"}, Error{Code: CodeProtected, Reason: "base", Service: "localmount"}},
		{Request{Kind: "enable", Service: "root"}, Error{Code: CodeProtected, Reason: "base", Service: "root"}},
		{Request{Kind: "stop", Service: "sysonly"}, Error{Code: CodeProtected, Reason: "base", Service: "sysonly"}},
		// the dependent that makes it a denial is the service, with its class as the reason
		{Request{Kind: "stop", Service: "networking"}, Error{Code: CodeProtected, Reason: "edelweiss", Service: "edelweiss"}},
		{Request{Kind: "restart", Service: "networking"}, Error{Code: CodeProtected, Reason: "edelweiss", Service: "edelweiss"}},
		{Request{Kind: "stop", Service: "blkmon"}, Error{Code: CodeProtected, Reason: "base", Service: "localmount"}},
		{Request{Kind: "restart", Service: "blkmon"}, Error{Code: CodeProtected, Reason: "base", Service: "localmount"}},
		// the start of a crashed service is a restart and stops what needs it; as a plain start (allowed) it would not
		{Request{Kind: "start", Service: "net.eth0"}, Error{Code: CodeProtected, Reason: "edelweiss", Service: "edelweiss"}},
	} {
		check(t, tc.req, nil, tc.want)
		tc.req.Confirm = true // a confirmation does not lift a denial
		check(t, tc.req, nil, tc.want)
	}
}

func TestDisklessCommits(t *testing.T) {
	for _, tc := range []struct {
		req   Request
		steps []string
	}{
		{Request{Kind: "enable", Service: "docker", Runlevel: "boot"}, []string{"exec /sbin/rc-update add docker boot"}},
		{Request{Kind: "disable", Service: "crond"}, []string{"exec /sbin/rc-update del crond default"}},
		{Request{Kind: "disable", Service: "both"}, []string{"exec /sbin/rc-update del both default", "exec /sbin/rc-update del both boot"}},
	} {
		for _, mode := range []host.Mode{host.Sys, host.Diskless, host.Data} {
			want := slices.Clone(tc.steps)
			if mode != host.Sys {
				want = append(want, "exec /usr/sbin/lbu commit") // once, after every rc-update
			}
			if p, err := build(world, newDeps(), tc.req, mode); err != nil || !slices.Equal(lines(p), want) {
				t.Errorf("%s %+v: %q, %v; want %q", mode, tc.req, lines(p), err, want)
			}
		}
	}
	// start, stop and restart change nothing in /etc
	for _, req := range []Request{{Kind: "start", Service: "nginx"}, {Kind: "stop", Service: "crond"}, {Kind: "restart", Service: "crond"}} {
		for _, mode := range []host.Mode{host.Diskless, host.Data} {
			if p, err := build(world, newDeps(), req, mode); err != nil || len(p.Steps) != 1 {
				t.Errorf("%s %+v: %q, %v; want the one step of the command", mode, req, lines(p), err)
			}
		}
	}
}

func TestNoOpenRC(t *testing.T) {
	d := newDeps()
	for _, snap := range []openrc.Snapshot{{}, {Services: world.Services}} {
		p, err := build(snap, d, Request{Kind: "stop", Service: "crond"}, host.Sys)
		wantRefusal(t, err, fail(CodeNoOpenRC))
		if !reflect.DeepEqual(p, Plan{}) || len(d.calls) != 0 {
			t.Errorf("without OpenRC: plan %+v, %d dependents lookups", p, len(d.calls))
		}
	}
}

func TestDisableAllRunlevels(t *testing.T) {
	for _, tc := range []struct {
		req   Request
		steps []string
		want  Error
	}{
		// default, then boot, whatever order the service lists them in; no runlevel: every one it is in
		{Request{Kind: "disable", Service: "both"}, []string{"exec /sbin/rc-update del both default", "exec /sbin/rc-update del both boot"}, Error{}},
		{Request{Kind: "disable", Service: "both", Runlevel: "boot"}, []string{"exec /sbin/rc-update del both boot"}, Error{}},
		{Request{Kind: "disable", Service: "both", Runlevel: "default"}, []string{"exec /sbin/rc-update del both default"}, Error{}},
		{Request{Kind: "disable", Service: "bootonly"}, []string{"exec /sbin/rc-update del bootonly boot"}, Error{}},
		// only default and boot are offered: nonetwork stays
		{Request{Kind: "disable", Service: "mixed"}, []string{"exec /sbin/rc-update del mixed default"}, Error{}},
		{Request{Kind: "disable", Service: "mixed", Runlevel: "nonetwork"}, nil, bad("runlevel")},
		// with a runlevel the service has to be in it
		{Request{Kind: "disable", Service: "crond", Runlevel: "boot"}, nil, bad("runlevel")},
		{Request{Kind: "disable", Service: "bootonly", Runlevel: "default"}, nil, bad("runlevel")},
		{Request{Kind: "disable", Service: "docker", Runlevel: "default"}, nil, bad("runlevel")},
		// without one it has to be in default or boot
		{Request{Kind: "disable", Service: "docker"}, nil, bad("kind")},
		{Request{Kind: "disable", Service: "sysonly"}, nil, bad("kind")},
	} {
		check(t, tc.req, tc.steps, tc.want)
	}
}

// TestRunlevels: enable and disable take default or boot, start, stop and restart take none; "already" for enable.
func TestRunlevels(t *testing.T) {
	for _, tc := range []struct {
		req   Request
		steps []string
		want  Error
	}{
		{Request{Kind: "enable", Service: "crond"}, nil, bad("kind")},                      // empty is default
		{Request{Kind: "enable", Service: "crond", Runlevel: "default"}, nil, bad("kind")}, // already in it
		{Request{Kind: "enable", Service: "both", Runlevel: "boot"}, nil, bad("kind")},
		{Request{Kind: "enable", Service: "docker", Runlevel: "sysinit"}, nil, bad("runlevel")},
		{Request{Kind: "enable", Service: "docker", Runlevel: "shutdown"}, nil, bad("runlevel")},
		{Request{Kind: "enable", Service: "docker", Runlevel: "nonetwork"}, nil, bad("runlevel")},
		{Request{Kind: "enable", Service: "docker", Runlevel: "Default"}, nil, bad("runlevel")},
		{Request{Kind: "enable", Service: "docker", Runlevel: " boot"}, nil, bad("runlevel")},
		{Request{Kind: "enable", Service: "docker", Runlevel: "../boot"}, nil, bad("runlevel")},
		{Request{Kind: "disable", Service: "crond", Runlevel: "sysinit"}, nil, bad("runlevel")},
		{Request{Kind: "disable", Service: "crond", Runlevel: "Default"}, nil, bad("runlevel")},
		// start, stop and restart take none, not even a valid one
		{Request{Kind: "start", Service: "nginx", Runlevel: "default"}, nil, bad("runlevel")},
		{Request{Kind: "stop", Service: "crond", Runlevel: "boot"}, nil, bad("runlevel")},
		{Request{Kind: "restart", Service: "crond", Runlevel: "default"}, nil, bad("runlevel")},
	} {
		check(t, tc.req, tc.steps, tc.want)
	}
}

// TestStates: what start, stop and restart do to a service in each state. A start of a crashed service is a restart.
func TestStates(t *testing.T) {
	const already = "already"
	for _, tc := range []struct {
		state                openrc.State
		start, stop, restart string
	}{
		{openrc.Started, already, "stop", "restart"},
		{openrc.Starting, already, "stop", "restart"},
		{openrc.Stopped, "start", already, "restart"},
		{openrc.Stopping, "start", already, "restart"},
		{openrc.Crashed, "restart", "stop", "restart"},
		{openrc.Failed, "start", "stop", "restart"},
		{openrc.Inactive, "start", "stop", "restart"},
	} {
		snap := snapshot(svc("thing", tc.state, "default"))
		for kind, want := range map[string]string{"start": tc.start, "stop": tc.stop, "restart": tc.restart} {
			p, err := build(snap, newDeps(), Request{Kind: kind, Service: "thing"}, host.Sys)
			if want == already {
				wantRefusal(t, err, bad("kind"))
				continue
			}
			if err != nil || p.Kind != want || !slices.Equal(lines(p), []string{"exec /sbin/rc-service thing " + want}) {
				t.Errorf("%s of a %s service: %s %q, %v; want %s", kind, tc.state, p.Kind, lines(p), err, want)
			}
		}
	}
}

func TestInvalidInput(t *testing.T) {
	for _, kind := range []string{"", "Start", "start ", "reload", "enable-all", "stop;start"} {
		check(t, Request{Kind: kind, Service: "crond"}, nil, bad("kind"))
	}
	for _, name := range []string{"", "../x", "a b", ".hidden", "-x", "a/b", "sshd\n", strings.Repeat("a", 65)} {
		check(t, Request{Kind: "stop", Service: name}, nil, bad("service"))
	}
	for _, name := range []string{"ghost", "SSHD", "crond2", "cron", "ssh", "edel"} { // valid names, not in the list
		check(t, Request{Kind: "stop", Service: name}, nil, fail(CodeNotFound))
	}
}

// TestAlreadyMessages: the message says which state the service is in, started or starting, stopped or stopping.
func TestAlreadyMessages(t *testing.T) {
	for _, tc := range []struct {
		state openrc.State
		kind  string
		text  string
	}{
		{openrc.Started, "start", "thing is already started"},
		{openrc.Starting, "start", "thing is already starting"},
		{openrc.Stopped, "stop", "thing is already stopped"},
		{openrc.Stopping, "stop", "thing is already stopping"},
	} {
		_, err := build(snapshot(svc("thing", tc.state, "default")), newDeps(), Request{Kind: tc.kind, Service: "thing"}, host.Sys)
		if e := wantRefusal(t, err, bad("kind")); e.Message != tc.text {
			t.Errorf("%s of a %s service: message %q, want %q", tc.kind, tc.state, e.Message, tc.text)
		}
	}
}

// TestUnknownMode: enable and disable need a known system mode, or they would not know whether to save with lbu. It is
// the last check, after the policy; start, stop and restart do not look at the mode.
func TestUnknownMode(t *testing.T) {
	for _, mode := range []host.Mode{"", "Sys", "live"} {
		for _, req := range []Request{
			{Kind: "enable", Service: "docker"}, {Kind: "disable", Service: "crond"}, {Kind: "disable", Service: "both"},
			{Kind: "disable", Service: "sshd", Confirm: true}, // a confirmed warning gets there too
		} {
			p, err := build(world, newDeps(), req, mode)
			if e := wantRefusal(t, err, fail(CodeFailed)); !strings.Contains(e.Message, string(mode)) {
				t.Errorf("mode %q, %+v: message %q, want the mode in it", mode, req, e.Message)
			}
			if !reflect.DeepEqual(p, Plan{}) {
				t.Errorf("mode %q, %+v: plan %+v", mode, req, p)
			}
		}
		for _, req := range []Request{{Kind: "start", Service: "nginx"}, {Kind: "stop", Service: "crond"}, {Kind: "restart", Service: "crond"}} {
			if p, err := build(world, newDeps(), req, mode); err != nil || len(p.Steps) != 1 {
				t.Errorf("mode %q, %+v: %q, %v; want the one step of the command", mode, req, lines(p), err)
			}
		}
		// every other check comes first
		for _, tc := range []struct {
			req  Request
			want Error
		}{
			{Request{Kind: "enable", Service: "crond"}, bad("kind")}, // already in default
			{Request{Kind: "enable", Service: "docker", Runlevel: "x"}, bad("runlevel")},
			{Request{Kind: "disable", Service: "edelweiss"}, Error{Code: CodeProtected, Reason: "edelweiss", Service: "edelweiss"}},
			{Request{Kind: "disable", Service: "sshd"}, Error{Code: CodeConfirm, Reason: "remote"}},
		} {
			_, err := build(world, newDeps(), tc.req, mode)
			wantRefusal(t, err, tc.want)
		}
	}
}

// A dependent that the list does not hold, or a dependents lookup that fails, is a failure (code failed), not a plan
// without the cascade.
func TestUnknownDependent(t *testing.T) {
	d := &fakeDeps{needsme: map[string][]string{"sshd": {"tunnel", "ghost"}}}
	p, err := build(world, d, Request{Kind: "stop", Service: "sshd", Confirm: true}, host.Sys)
	e := wantRefusal(t, err, fail(CodeFailed))
	if !strings.Contains(e.Message, "ghost") || !strings.Contains(e.Message, "changed") {
		t.Errorf("message %q: want the missing name and that the list changed", e.Message)
	}
	if !reflect.DeepEqual(p, Plan{}) {
		t.Errorf("plan %+v", p)
	}
}

func TestDependentsError(t *testing.T) {
	d := &fakeDeps{err: errors.New("boom")}
	_, err := build(world, d, Request{Kind: "restart", Service: "crond"}, host.Sys)
	if e := wantRefusal(t, err, fail(CodeFailed)); !strings.Contains(e.Message, "boom") {
		t.Errorf("message %q: want the cause in it", e.Message)
	}
}

func TestDependentsGetTheContext(t *testing.T) {
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "mine")
	d := newDeps()
	if _, err := Build(ctx, world, d.dependents, Request{Kind: "stop", Service: "crond"}, Env{Tools: tools}); err != nil {
		t.Fatal(err)
	}
	if d.ctx == nil || d.ctx.Value(key{}) != "mine" {
		t.Errorf("the dependents lookup got the context %v, want the one of Build", d.ctx)
	}
}

// Only stop, restart and the start of a crashed service (a restart) look at the dependents.
func TestDependentsOnlyWhereNeeded(t *testing.T) {
	for _, tc := range []struct {
		req   Request
		calls []string
	}{
		{Request{Kind: "start", Service: "nginx"}, nil},
		{Request{Kind: "enable", Service: "docker"}, nil},
		{Request{Kind: "disable", Service: "crond"}, nil},
		{Request{Kind: "stop", Service: "crond"}, []string{"crond"}},
		{Request{Kind: "restart", Service: "crond"}, []string{"crond"}},
		{Request{Kind: "start", Service: "chronyd"}, []string{"chronyd"}},
	} {
		d := newDeps()
		if _, err := build(world, d, tc.req, host.Sys); err != nil {
			t.Errorf("%+v: %v", tc.req, err)
		}
		if !slices.Equal(d.calls, tc.calls) {
			t.Errorf("%+v: dependents asked for %q, want %q", tc.req, d.calls, tc.calls)
		}
	}
}

// A start of a crashed service is a restart: OpenRC's start does nothing to it. It is decided as one, planned as one.
func TestCrashedStartIsRestart(t *testing.T) {
	p, err := build(world, newDeps(), Request{Kind: "start", Service: "chronyd"}, host.Sys)
	want := Plan{Kind: "restart", Target: "chronyd", Steps: []jobs.Step{step("restart", tools.Restart("chronyd"), "chronyd", jobs.MarkStart)}}
	if err != nil || !reflect.DeepEqual(p, want) {
		t.Errorf("start of a crashed service:\n got %+v, %v\nwant %+v", p, err, want)
	}

	// dropbear: start is allowed, restart warns, and the running dependent goes down with it
	r := Request{Kind: "start", Service: "dropbear"}
	_, err = build(world, newDeps(), r, host.Sys)
	wantRefusal(t, err, Error{Code: CodeConfirm, Reason: "remote", Services: []string{"tunnel"}})
	r.Confirm = true
	p, err = build(world, newDeps(), r, host.Sys)
	if err != nil || p.Kind != "restart" || !slices.Equal(lines(p), []string{"exec /sbin/rc-service dropbear restart"}) {
		t.Errorf("confirmed start of a crashed dropbear: %s %q, %v", p.Kind, lines(p), err)
	}

	// edelweiss: start is denied, restart allowed
	snap := snapshot(svc("edelweiss", openrc.Crashed, "default"))
	p, err = build(snap, newDeps(), Request{Kind: "start", Service: "edelweiss"}, host.Sys)
	if err != nil || p.Kind != "restart" {
		t.Errorf("start of a crashed edelweiss: %+v, %v; want a restart", p, err)
	}
	// stopping it stays a stop
	p, err = build(world, newDeps(), Request{Kind: "stop", Service: "chronyd"}, host.Sys)
	if err != nil || p.Kind != "stop" {
		t.Errorf("stop of a crashed service: %+v, %v", p, err)
	}
}

// TestCheckOrder: each check comes before the next, each with its own refusal.
func TestCheckOrder(t *testing.T) {
	for _, tc := range []struct {
		name  string
		snap  openrc.Snapshot
		req   Request
		needs map[string][]string
		err   error
		want  Error
		calls int // asks for the dependents
	}{
		{"OpenRC before the kind", openrc.Snapshot{Services: world.Services},
			Request{Kind: "nope", Service: "../x", Runlevel: "x"}, nil, nil, fail(CodeNoOpenRC), 0},
		{"kind before the name", world, Request{Kind: "nope", Service: "../x", Runlevel: "x"}, nil, nil, bad("kind"), 0},
		{"name before not found", world, Request{Kind: "stop", Service: "../x", Runlevel: "x"}, nil, nil, bad("service"), 0},
		{"not found before the runlevel", world, Request{Kind: "start", Service: "ghost", Runlevel: "x"}, nil, nil, fail(CodeNotFound), 0},
		{"runlevel before already started", world, Request{Kind: "start", Service: "sshd", Runlevel: "default"}, nil, nil, bad("runlevel"), 0},
		{"runlevel before already in it", world, Request{Kind: "enable", Service: "crond", Runlevel: "sysinit"}, nil, nil, bad("runlevel"), 0},
		{"already started before protected", world, Request{Kind: "start", Service: "edelweiss"}, nil, nil, bad("kind"), 0},
		{"already stopped before protected", world, Request{Kind: "stop", Service: "root"}, nil, nil, bad("kind"), 0},
		{"already enabled before protected", world, Request{Kind: "enable", Service: "edelweiss", Runlevel: "default"}, nil, nil, bad("kind"), 0},
		{"no runlevel to leave before protected", world, Request{Kind: "disable", Service: "root"}, nil, nil, bad("kind"), 0},
		{"dependents before protected", world, Request{Kind: "stop", Service: "edelweiss"}, needsme, errors.New("boom"), fail(CodeFailed), 1},
		{"unknown dependent before protected", world, Request{Kind: "stop", Service: "localmount"},
			map[string][]string{"localmount": {"ghost"}}, nil, fail(CodeFailed), 1},
		{"protected before confirm", world, Request{Kind: "stop", Service: "networking"}, needsme, nil,
			Error{Code: CodeProtected, Reason: "edelweiss", Service: "edelweiss"}, 1},
	} {
		d := &fakeDeps{needsme: tc.needs, err: tc.err}
		p, err := build(tc.snap, d, tc.req, host.Sys)
		wantRefusal(t, err, tc.want)
		if !reflect.DeepEqual(p, Plan{}) || len(d.calls) != tc.calls {
			t.Errorf("%s: plan %+v, %d dependents lookups, want %d", tc.name, p, len(d.calls), tc.calls)
		}
	}
}
