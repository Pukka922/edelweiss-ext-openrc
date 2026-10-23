//go:build integration

package integration

import (
	"context"
	"errors"
	"os"
	"os/user"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/actions"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/host"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/jobs"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/logs"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/openrc"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/run"
)

// The packages of the extension against the real OpenRC: what openrc.Reader and internal/logs read on "/", and the
// plans of internal/actions done step by step by a jobs.Executor in the test process, as the runner of a job does them.

// reader reads the services of the container as the extension reads them on a host.
func reader() openrc.Reader {
	return openrc.Reader{Root: "/", Runner: exe, Tools: openrc.FindTools("/")}
}

// service is the service of that name in a fresh read of the services.
func service(t *testing.T, r openrc.Reader, name string) openrc.Service {
	t.Helper()
	snap, err := r.Read(ctx)
	if err != nil {
		t.Fatalf("reading the services: %v", err)
	}
	if !snap.Present {
		t.Fatal("OpenRC is not present")
	}
	i := slices.IndexFunc(snap.Services, func(s openrc.Service) bool { return s.Name == name })
	if i < 0 {
		t.Fatalf("%s is not in the list", name)
	}
	return snap.Services[i]
}

// actor changes the services as the extension does: it plans a request on a fresh read (POST /jobs) and does the steps
// of the plan with the executor of the runner, which marks the outcome of the starts and stops where the view reads it.
type actor struct {
	t   *testing.T
	r   openrc.Reader
	env actions.Env
	x   jobs.Executor
}

func newActor(t *testing.T) actor {
	t.Helper()
	mode, err := host.FS{Root: "/"}.Mode()
	if err != nil {
		t.Fatalf("the system mode: %v", err)
	}
	r := reader()
	return actor{t: t, r: r, env: actions.Env{Mode: mode, Tools: r.Tools},
		x: jobs.Executor{Root: "/", Runner: exe, MarkDir: jobs.DefaultMarkDir}}
}

// do plans the request and does its steps up to the first that fails; a request the planner refuses fails the test.
func (a actor) do(kind, name string) (actions.Plan, error) {
	a.t.Helper()
	snap, err := a.r.Read(ctx)
	if err != nil {
		a.t.Fatalf("reading the services: %v", err)
	}
	plan, err := actions.Build(ctx, snap, a.r.Dependents, actions.Request{Kind: kind, Service: name}, a.env)
	if err != nil {
		a.t.Fatalf("planning %s %s: %v", kind, name, err)
	}
	for _, s := range plan.Steps {
		if err := a.x.Do(ctx, s); err != nil {
			return plan, err
		}
	}
	return plan, nil
}

// must is do for a request whose steps all have to work.
func (a actor) must(kind, name string) actions.Plan {
	a.t.Helper()
	plan, err := a.do(kind, name)
	if err != nil {
		var se *jobs.StepError
		if errors.As(err, &se) {
			a.t.Fatalf("%s %s: %v\n%s", kind, name, err, se.Output)
		}
		a.t.Fatalf("%s %s: %v", kind, name, err)
	}
	return plan
}

// state checks the state of each of the services in a fresh read.
func (a actor) state(want openrc.State, names ...string) {
	a.t.Helper()
	for _, name := range names {
		if got := service(a.t, a.r, name).State; got != want {
			a.t.Errorf("%s is %s, want %s", name, got, want)
		}
	}
}

// marks are the markers of the executor: the services whose last start failed, and those whose last start or stop
// worked.
func marks(t *testing.T) (failed, ok map[string]time.Time) {
	t.Helper()
	failed, ok, err := jobs.ReadMarks("/", jobs.DefaultMarkDir)
	if err != nil {
		t.Fatalf("reading the markers: %v", err)
	}
	return failed, ok
}

// TestActionsAgainstOpenRC starts, restarts and stops the test services, switches the autostart and makes a start fail,
// all through the plans of internal/actions, and checks what OpenRC did by what openrc.Reader reads afterwards.
func TestActionsAgainstOpenRC(t *testing.T) {
	a := newActor(t)

	a.must("start", "t-daemon")
	s := service(t, a.r, "t-daemon")
	if s.State != openrc.Started || s.PID == 0 || s.PID != readPID(t, "/run/t-daemon.pid") {
		t.Errorf("after the start t-daemon is %s with pid %d, want started with the pid of /run/t-daemon.pid", s.State, s.PID)
	}
	if _, ok := marks(t); ok["t-daemon"].IsZero() {
		t.Errorf("no ok marker for t-daemon after its start: %v", ok)
	}

	a.must("restart", "t-daemon")
	if again := service(t, a.r, "t-daemon"); again.State != openrc.Started || again.PID == 0 || again.PID == s.PID {
		t.Errorf("after the restart t-daemon is %s with pid %d, want started by a new daemon (not %d)", again.State,
			again.PID, s.PID)
	}

	// OpenRC stops the services that need the service first: the plan of the stop of t-daemon takes t-needs along
	a.must("start", "t-needs")
	a.state(openrc.Started, "t-daemon", "t-needs")
	a.must("stop", "t-daemon")
	a.state(openrc.Stopped, "t-daemon", "t-needs")

	a.must("enable", "t-daemon")
	if fi, err := os.Lstat("/etc/runlevels/default/t-daemon"); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("after the enable /etc/runlevels/default/t-daemon is no symlink: %v", err)
	}
	if got := service(t, a.r, "t-daemon").Runlevels; !slices.Equal(got, []string{"default"}) {
		t.Errorf("after the enable t-daemon is in %v, want [default]", got)
	}
	a.must("disable", "t-daemon")
	if _, err := os.Lstat("/etc/runlevels/default/t-daemon"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("after the disable /etc/runlevels/default/t-daemon is still there: %v", err)
	}
	if got := service(t, a.r, "t-daemon").Runlevels; len(got) != 0 {
		t.Errorf("after the disable t-daemon is in %v, want none", got)
	}

	// a start that fails: the step fails with what OpenRC printed, the service stays stopped, the marker remembers it
	_, err := a.do("start", "t-fail")
	var se *jobs.StepError
	if !errors.As(err, &se) || !strings.Contains(se.Output, "boom") {
		t.Errorf("the start of t-fail: %v, want a failed step with the output of OpenRC, which says boom", err)
	} else {
		t.Logf("the failed start of t-fail: %s\n%s", se.Message, se.Output)
	}
	a.state(openrc.Stopped, "t-fail")
	if failed, _ := marks(t); failed["t-fail"].IsZero() {
		t.Errorf("no failed marker for t-fail after its failed start: %v", failed)
	}

	// a daemon that died: crashed; its start is planned as the restart that brings it back
	a.must("start", "t-crash")
	kill(t, readPID(t, "/run/t-crash.pid"))
	a.state(openrc.Crashed, "t-crash")
	if plan := a.must("start", "t-crash"); plan.Kind != "restart" {
		t.Errorf("the start of the crashed t-crash is planned as %s, want restart", plan.Kind)
	}
	a.state(openrc.Started, "t-crash")

	a.must("start", "t-oneshot")
	if s := service(t, a.r, "t-oneshot"); s.State != openrc.Started || !s.OneShot || s.PID != 0 {
		t.Errorf("t-oneshot is %s, one-shot %v, pid %d; want started, one-shot, no pid", s.State, s.OneShot, s.PID)
	}
}

// mustRun runs a command of the test (rc-update, openrc, logger), which has to work.
func mustRun(t *testing.T, path string, args ...string) string {
	t.Helper()
	out, err := exe.Run(ctx, run.Cmd{Path: path, Args: args, Timeout: time.Minute})
	if err != nil {
		t.Fatalf("%v", err)
	}
	return out
}

// logSources are the log sources of the service in a fresh read of the services, and the names under which the system
// log has its lines (as the API looks them up for a log request).
func logSources(t *testing.T, r openrc.Reader, name string) ([]logs.Source, []string) {
	t.Helper()
	programs := r.Programs(name)
	return logs.Sources("/", service(t, r, name), programs), programs
}

// pick is the source of that id among the log sources of the service.
func pick(t *testing.T, sources []logs.Source, name, id string) logs.Source {
	t.Helper()
	i := slices.IndexFunc(sources, func(s logs.Source) bool { return s.ID == id })
	if i < 0 {
		t.Fatalf("%s has no log source %s: %+v", name, id, sources)
	}
	return sources[i]
}

// TestLogsAgainstOpenRC reads the logs of the test services as the page does: the failed start of a runlevel change
// in the system log (the red line of a service that did not come up at boot), the output_log of t-log, tail and follow,
// and the lines of t-daemon in /var/log/messages of busybox syslogd.
func TestLogsAgainstOpenRC(t *testing.T) {
	r := reader()

	// First, in the fresh container: a runlevel change stops the services that were started by hand.
	t.Run("boot failure", func(t *testing.T) {
		boot := time.Now().Add(-time.Second) // syslogd writes whole seconds
		if failed, _, err := logs.BootFailures("/", boot, time.Now()); err != nil || len(failed) != 0 {
			t.Fatalf("before the runlevel change: %v, %v; want no failed start", failed, err)
		}
		mustRun(t, findTool(t, "rc-update"), "add", "t-fail", "default")
		t.Logf("openrc default:\n%s", mustRun(t, findTool(t, "openrc"), "default"))
		waitFor(t, "the failed start is in /var/log/messages", func() bool {
			return fileHas("/var/log/messages", "ERROR: t-fail failed to start")
		})
		if failed, _, err := logs.BootFailures("/", boot, time.Now()); err != nil || failed["t-fail"].IsZero() {
			t.Errorf("after the runlevel change: %v, %v; want the failed start of t-fail", failed, err)
		}
		// busybox syslogd, started by the setup, wrote its start line: the lines after it count whatever their time,
		// so a boot time after the failure (the clock of a host without a real-time clock) does not hide it
		later := time.Now().Add(time.Hour)
		if failed, byBoot, err := logs.BootFailures("/", later, later); err != nil || byBoot || failed["t-fail"].IsZero() {
			t.Errorf("with a boot time after the failure: %v, by the boot time %v, %v; want the failed start of t-fail "+
				"after the start line of syslogd", failed, byBoot, err)
		}
	})

	// The output_log of t-log, as tail and follow read it: the source and its last lines are what both need.
	mustRun(t, r.Tools.RcService, "t-log", "start")
	sources, programs := logSources(t, r, "t-log")
	ids := []string{}
	for _, s := range sources {
		ids = append(ids, s.ID)
	}
	if !slices.Equal(ids, []string{"output", "error", "syslog"}) {
		t.Errorf("the log sources of t-log are %v, want output, error, syslog: %+v", ids, sources)
	}
	out := pick(t, sources, "t-log", "output")
	want := logs.Source{ID: "output", Label: "/var/log/t-log.log", Path: "/var/log/t-log.log", Readable: true}
	if out != want {
		t.Errorf("the output_log of t-log is %+v, want %+v", out, want)
	}
	waitFor(t, "t-log wrote its output", func() bool { return fileHas("/var/log/t-log.log", "tick") })
	tail, err := logs.Tail("/", out, programs, 500)
	if err != nil {
		t.Fatalf("tail of the output_log of t-log: %v", err)
	}
	t.Run("tail", func(t *testing.T) {
		if len(tail.Lines) == 0 || slices.ContainsFunc(tail.Lines, func(l string) bool { return l != "tick" }) {
			t.Errorf("tail of the output_log of t-log: %q, want tick lines", tail.Lines)
		}
	})
	t.Run("follow", func(t *testing.T) {
		follow, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		var events []logs.Event
		logs.Follow(follow, "/", out, programs, tail.Offset, 100*time.Millisecond, func(e logs.Event) bool {
			events = append(events, e)
			return e.Kind != "line"
		})
		if len(events) != 1 || events[0] != (logs.Event{Kind: "line", Line: "tick"}) {
			t.Errorf("following the output_log of t-log from %d: %+v, want the next tick line", tail.Offset, events)
		}
		// every line is a tick: the one followed is new when the file has it after the offset of the tail
		if st, err := os.Stat("/var/log/t-log.log"); err != nil || st.Size() < tail.Offset+int64(len("tick\n")) {
			t.Errorf("the line followed is not after the offset %d of the tail: %v", tail.Offset, err)
		}
	})

	t.Run("syslog", func(t *testing.T) {
		logger := findTool(t, "logger")
		mustRun(t, logger, "-t", "t-daemon", "x")
		mustRun(t, logger, "-t", "t-other", "y")
		waitFor(t, "the lines are in /var/log/messages", func() bool { return fileHas("/var/log/messages", "t-other: y") })
		sources, programs := logSources(t, r, "t-daemon")
		lines, err := logs.Tail("/", pick(t, sources, "t-daemon", "syslog"), programs, 500)
		if err != nil {
			t.Fatalf("tail of the system log of t-daemon: %v", err)
		}
		if !slices.ContainsFunc(lines.Lines, func(l string) bool { return strings.HasSuffix(l, " t-daemon: x") }) ||
			slices.ContainsFunc(lines.Lines, func(l string) bool { return strings.Contains(l, "t-other") }) {
			t.Errorf("the system log of t-daemon: %q, want its line x and none of t-other", lines.Lines)
		}
	})
}

// TestReadAsUser reads the services as the extension does for a user without admin mode: scripts/test-alpine.sh runs
// it as nobody, after it started t-daemon, t-needs and t-crash as root and killed the daemon of t-crash. The view and
// the details of t-daemon work, with the states that root sees.
func TestReadAsUser(t *testing.T) {
	u, err := user.Current()
	if err != nil || u.Username != "nobody" {
		t.Fatalf("runs as %+v (%v); scripts/test-alpine.sh runs it as nobody", u, err)
	}
	t.Logf("runs as %s (uid %s, euid %d)", u.Username, u.Uid, os.Geteuid())
	r := reader()
	for name, want := range map[string]openrc.State{"t-daemon": openrc.Started, "t-needs": openrc.Started,
		"t-crash": openrc.Crashed, "t-fail": openrc.Stopped} {
		if got := service(t, r, name).State; got != want {
			t.Errorf("%s is %s, want %s", name, got, want)
		}
	}
	if pid := service(t, r, "t-daemon").PID; pid == 0 || pid != readPID(t, "/run/t-daemon.pid") {
		t.Errorf("t-daemon has pid %d, want the one of /run/t-daemon.pid", pid)
	}
	d, err := r.Details(ctx, "t-daemon")
	if err != nil {
		t.Fatalf("the details of t-daemon: %v", err)
	}
	want := openrc.Details{Needs: []string{}, Uses: []string{}, After: []string{}, NeededBy: []string{"t-needs"}}
	if !slices.Equal(d.Needs, want.Needs) || !slices.Equal(d.Uses, want.Uses) || !slices.Equal(d.After, want.After) ||
		!slices.Equal(d.NeededBy, want.NeededBy) {
		t.Errorf("the details of t-daemon are %+v, want %+v", d, want)
	}
}
