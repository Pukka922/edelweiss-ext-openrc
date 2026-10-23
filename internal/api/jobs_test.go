package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/actions"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/jobs"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/openrc"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/run"
)

var idRe = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}-[0-9a-f]{4}$`)

// restartNginx is a request that plans: nginx is free.
var restartNginx = actions.Request{Kind: "restart", Service: "nginx"}

// holdJobs makes the commands of the jobs wait until the returned func is called (the cleanup calls it, too).
func (f *harness) holdJobs() (release func()) {
	f.gate = make(chan struct{})
	var once sync.Once
	release = func() { once.Do(func() { close(f.gate) }) }
	f.t.Cleanup(release) // runs before the wait for the launcher
	return release
}

// assertLockFree fails unless the job lock can be taken, which means no process holds it.
func (f *harness) assertLockFree() {
	f.t.Helper()
	lock, err := f.api.Jobs.TryLock()
	if err != nil {
		f.t.Fatalf("the job lock is held: %v", err)
	}
	lock.Close()
}

// runJob makes a job of the request through the API and waits for its runner to end; it returns the state it ended in.
func (f *harness) runJob(req any) jobs.State {
	f.t.Helper()
	st := decode[jobs.State](f.t, f.send("POST", "/jobs", req, admin).want(f.t, http.StatusAccepted))
	f.launcher.wg.Wait()
	end, err := f.api.Jobs.Get(st.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return end
}

// assertNothingStarted fails when a job was launched or made, or the job lock is held.
func (f *harness) assertNothingStarted() {
	f.t.Helper()
	if n := len(f.launcher.launched()); n != 0 {
		f.t.Errorf("%d jobs launched", n)
	}
	if st, err := f.api.Jobs.Newest(); st != nil || err != nil {
		f.t.Errorf("a refused request left the job %+v, %v", st, err)
	}
	f.assertLockFree()
}

func TestPostJobNeedsAdmin(t *testing.T) {
	f := newHarness(t)
	f.send("POST", "/jobs", restartNginx, plain).wantError(t, http.StatusForbidden, "admin_required")
	if n := len(f.host.Commands()); n != 0 {
		t.Errorf("a request without admin mode ran %v", f.host.Commands())
	}
	f.assertNothingStarted()
}

func TestPostJobInvalidJSON(t *testing.T) {
	f := newHarness(t)
	for _, body := range []string{"", "{", "[]", `{"kind": 5}`, `{"kind":"stop"} {`} {
		f.send("POST", "/jobs", body, admin).wantError(t, http.StatusBadRequest, "invalid_json")
	}
	f.assertNothingStarted()
}

func TestPostJobBusy(t *testing.T) {
	f := newHarness(t)
	lock, err := f.api.Jobs.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	e := f.send("POST", "/jobs", restartNginx, admin).wantError(t, http.StatusConflict, "busy")
	if e["message"] != "another change is running; try again" {
		t.Errorf("message %q", e["message"])
	}
	if n := len(f.host.Commands()) + len(f.launcher.launched()); n != 0 {
		t.Errorf("a busy request went on: %v, %d launches", f.host.Commands(), len(f.launcher.launched()))
	}
	if !f.api.Jobs.Locked() { // the lock of the other process is not touched
		t.Error("the lock of the running job was released")
	}
	lock.Close()
	f.assertLockFree()
}

// A risky change needs the confirmation: the refusal names the reason and the running services that stop along.
func TestPostJobConfirmRequired(t *testing.T) {
	f := newHarness(t)
	e := f.send("POST", "/jobs", actions.Request{Kind: "stop", Service: "networking"}, admin).wantError(t,
		http.StatusConflict, "confirm_required")
	if e["reason"] != "network" || !reflect.DeepEqual(e["services"], []any{"nginx", "sshd"}) {
		t.Errorf("error %v; want the reason network and nginx, sshd", e)
	}
	e = f.send("POST", "/jobs", actions.Request{Kind: "stop", Service: "sshd"}, admin).wantError(t, http.StatusConflict,
		"confirm_required")
	if e["reason"] != "remote" || !reflect.DeepEqual(e["services"], []any{}) {
		t.Errorf("error %v; want the reason remote and no services", e)
	}
	f.assertNothingStarted()

	end := f.runJob(actions.Request{Kind: "stop", Service: "networking", Confirm: true})
	if end.Status != jobs.Done || end.Kind != "stop" || end.Target != "networking" {
		t.Errorf("the confirmed job %+v", end)
	}
	if got := f.cmds.Commands(); !slices.Equal(got, []string{rcService + " networking stop"}) {
		t.Errorf("the job ran %v", got)
	}
}

// A denied change names the protected service (the target, or the dependent that stops along) and its class.
func TestPostJobProtected(t *testing.T) {
	f := newHarness(t)
	for _, c := range []struct {
		req             actions.Request
		reason, service string
	}{
		{actions.Request{Kind: "stop", Service: "edelweiss", Confirm: true}, "edelweiss", "edelweiss"},
		{actions.Request{Kind: "stop", Service: "proxy"}, "edelweiss", "edelweiss"}, // edelweiss needs proxy
		{actions.Request{Kind: "restart", Service: "devfs", Confirm: true}, "base", "devfs"},
		{actions.Request{Kind: "disable", Service: "edelweiss"}, "edelweiss", "edelweiss"},
	} {
		e := f.send("POST", "/jobs", c.req, admin).wantError(t, http.StatusConflict, "protected")
		if e["reason"] != c.reason || e["service"] != c.service || e["services"] != nil {
			t.Errorf("%+v: error %v; want the reason %s and the service %s", c.req, e, c.reason, c.service)
		}
	}
	f.assertNothingStarted()
}

// The refusals of the planner keep their code, with the field of an invalid input.
func TestPostJobRefusals(t *testing.T) {
	for _, c := range []struct {
		name   string
		req    actions.Request
		status int
		code   string
		field  string
	}{
		{"unknown kind", actions.Request{Kind: "bogus", Service: "nginx"}, 400, "invalid_input", "kind"},
		{"invalid name", actions.Request{Kind: "stop", Service: "../x"}, 400, "invalid_input", "service"},
		{"already started", actions.Request{Kind: "start", Service: "nginx"}, 400, "invalid_input", "kind"},
		{"no such runlevel", actions.Request{Kind: "enable", Service: "cron", Runlevel: "sysinit"}, 400, "invalid_input",
			"runlevel"},
		{"no such service", actions.Request{Kind: "stop", Service: "nosuch"}, 404, "not_found", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newHarness(t)
			e := f.send("POST", "/jobs", c.req, admin).wantError(t, c.status, c.code)
			if field, _ := e["field"].(string); field != c.field {
				t.Errorf("error %v; want the field %q", e, c.field)
			}
			f.assertNothingStarted()
		})
	}
}

// The modes of the host: a diskless one (root in RAM) and a data one (/var on a disk).
const (
	diskless = "tmpfs / tmpfs rw 0 0\n"
	data     = "tmpfs / tmpfs rw 0 0\n/dev/sda3 /var ext4 rw 0 0\n"
)

// Before the plan: a mode that cannot be read, or a change of the autostart in diskless and data mode without lbu, are
// failures of the host; a host without OpenRC or without rc-service or rc-update has no OpenRC to change.
func TestPostJobChecks(t *testing.T) {
	enableCron := actions.Request{Kind: "enable", Service: "cron"}
	disableDocker := actions.Request{Kind: "disable", Service: "docker"}
	for _, c := range []struct {
		name   string
		prep   func(f *harness)
		req    actions.Request
		status int
		code   string
	}{
		{"mode unreadable", func(f *harness) { f.remove("/proc/mounts") }, restartNginx, 500, "failed"},
		{"diskless without lbu", func(f *harness) { f.write("/proc/mounts", diskless); f.remove(lbu) }, enableCron, 500,
			"failed"},
		{"data without lbu", func(f *harness) { f.write("/proc/mounts", data); f.remove(lbu) }, disableDocker, 500, "failed"},
		{"no OpenRC", func(f *harness) { f.remove("/run/openrc/softlevel") }, restartNginx, 409, "no_openrc"},
		{"no rc-service", func(f *harness) { f.remove(rcService) }, restartNginx, 409, "no_openrc"},
		{"no rc-update", func(f *harness) { f.remove(rcUpdate) }, restartNginx, 409, "no_openrc"},
		{"the mode first", func(f *harness) { f.remove("/proc/mounts"); f.remove("/run/openrc/softlevel") }, restartNginx,
			500, "failed"},
		{"rc-status fails", func(f *harness) {
			f.host.Results[rcStatus+" --all --nocolor"] = run.FakeResult{Status: 1}
		}, restartNginx, 500, "failed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newHarness(t)
			c.prep(f)
			e := f.send("POST", "/jobs", c.req, admin).wantError(t, c.status, c.code)
			if strings.Contains(c.name, "lbu") && !strings.Contains(e["message"].(string), "lbu") {
				t.Errorf("message %q; want it to name lbu", e["message"])
			}
			f.assertNothingStarted()
		})
	}
}

// lbu saves a change of the autostart only: without it, a start, stop or restart in diskless and data mode is planned
// all the same, and so is everything on a sys host.
func TestPostJobWithoutLbu(t *testing.T) {
	for _, c := range []struct {
		name, mounts string
		req          actions.Request
		ran          string
	}{
		{"diskless start", diskless, actions.Request{Kind: "start", Service: "docker"}, rcService + " docker start"},
		{"data stop", data, actions.Request{Kind: "stop", Service: "nginx"}, rcService + " nginx stop"},
		{"diskless restart", diskless, restartNginx, rcService + " nginx restart"},
		{"sys enable", "/dev/sda2 / ext4 rw 0 0\n", actions.Request{Kind: "enable", Service: "cron"},
			rcUpdate + " add cron default"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newHarness(t)
			f.write("/proc/mounts", c.mounts)
			f.remove(lbu)
			if end := f.runJob(c.req); end.Status != jobs.Done {
				t.Errorf("job %+v", end)
			}
			if got := f.cmds.Commands(); !slices.Equal(got, []string{c.ran}) {
				t.Errorf("the job ran %v; want %s", got, c.ran)
			}
		})
	}
}

// In diskless mode a change of the autostart is saved with lbu commit.
func TestPostJobDisklessCommits(t *testing.T) {
	f := newHarness(t)
	f.write("/proc/mounts", diskless)
	end := f.runJob(actions.Request{Kind: "enable", Service: "cron"})
	if end.Status != jobs.Done || !slices.Equal(end.Steps, []string{"enable", "lbu"}) {
		t.Errorf("job %+v", end)
	}
	if got := f.cmds.Commands(); !slices.Equal(got, []string{rcUpdate + " add cron default", lbu + " commit"}) {
		t.Errorf("the job ran %v", got)
	}
}

func TestPostJobLaunches(t *testing.T) {
	f := newHarness(t)
	release := f.holdJobs()
	statusRuns := f.ran(rcStatus)
	st := decode[jobs.State](t, f.send("POST", "/jobs", restartNginx, admin).want(t, http.StatusAccepted))
	if !idRe.MatchString(st.ID) || st.Kind != "restart" || st.Target != "nginx" || st.Status != jobs.Running ||
		!slices.Equal(st.Steps, []string{"restart"}) {
		t.Fatalf("state %+v", st)
	}
	if n := f.ran(rcStatus); n != statusRuns+1 {
		t.Errorf("rc-status ran %d times for the request; want the services read afresh", n-statusRuns)
	}
	// the plan: the command for the name of the list, marked as a start of nginx
	dir := filepath.Join(f.api.Jobs.Base, st.ID)
	data, err := os.ReadFile(filepath.Join(dir, "spec.json"))
	if err != nil {
		t.Fatal(err)
	}
	var spec jobs.Spec
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	cmd := openrc.FindTools(f.root).Restart("nginx")
	want := []jobs.Step{{Op: jobs.OpExec, Label: "restart", Cmd: &cmd, Service: "nginx", Mark: jobs.MarkStart}}
	if !reflect.DeepEqual(spec.Steps, want) {
		t.Errorf("steps %+v; want %+v", spec.Steps, want)
	}
	if got := f.launcher.launched(); !slices.Equal(got, []string{dir}) {
		t.Fatalf("launches %v; want %s", got, dir)
	}
	// the job holds the lock now; nobody else starts a change meanwhile, and everybody sees it
	if !f.api.Jobs.Locked() {
		t.Error("the job does not hold the lock")
	}
	f.send("POST", "/jobs", restartNginx, admin).wantError(t, http.StatusConflict, "busy")
	if v := f.view(); v.Job == nil || v.Job.ID != st.ID || v.Job.Status != jobs.Running {
		t.Errorf("the view shows the job %+v; want the running one", v.Job)
	}

	release()
	f.launcher.wg.Wait()
	end, err := f.api.Jobs.Get(st.ID)
	if err != nil || end.Status != jobs.Done || end.Finished == nil {
		t.Fatalf("the job ended as %+v, %v", end, err)
	}
	if got := f.cmds.Commands(); !slices.Equal(got, []string{rcService + " nginx restart"}) {
		t.Errorf("the job ran %v", got)
	}
	// the API process closed its own descriptor of the lock: with the runner gone, nothing holds it
	f.assertLockFree()
	if _, err := os.Stat(f.path(jobs.DefaultMarkDir + "/ok/nginx")); err != nil {
		t.Errorf("the runner left no ok marker where the view reads them: %v", err)
	}
}

// A start that failed in a job shows as failed in the view (the marker of the runner), until a start works.
func TestPostJobFailedStart(t *testing.T) {
	f := newHarness(t)
	f.cmds.Results[rcService+" docker start"] = run.FakeResult{Output: " * docker: port in use", Status: 1}
	end := f.runJob(actions.Request{Kind: "start", Service: "docker"})
	if end.Status != jobs.Failed || end.Code != "failed" || !strings.Contains(end.Output, "port in use") {
		t.Errorf("job %+v; want it failed with the output", end)
	}
	v := f.view()
	if s := byName(v)["docker"]; s.State != "failed" || s.AutostartIdle {
		t.Errorf("docker %+v; want failed, not idle", s)
	}
	if v.Counts.Problems != 6 {
		t.Errorf("%d problems; want docker among them", v.Counts.Problems)
	}
	delete(f.cmds.Results, rcService+" docker start")
	if end := f.runJob(actions.Request{Kind: "start", Service: "docker"}); end.Status != jobs.Done {
		t.Fatalf("job %+v", end)
	}
	if s := byName(f.view())["docker"]; s.State != "stopped" || !s.AutostartIdle {
		t.Errorf("docker %+v; want stopped again (the fake rc-status) and idle", s)
	}
}

func TestPostJobLaunchFails(t *testing.T) {
	f := newHarness(t)
	f.launcher.err = errors.New("fork/exec /proc/self/exe: no such file or directory")
	e := f.send("POST", "/jobs", restartNginx, admin).wantError(t, http.StatusInternalServerError, "failed")
	if msg, _ := e["message"].(string); !strings.Contains(msg, "no such file or directory") {
		t.Errorf("message %q; want the reason", msg)
	}
	// the job was made: it ended as failed, with the reason, and not as an interrupted one
	st, err := f.api.Jobs.Newest()
	if err != nil || st == nil {
		t.Fatalf("Newest = %+v, %v", st, err)
	}
	if st.Status != jobs.Failed || st.Code != "failed" || !strings.Contains(st.Error, "no such file or directory") ||
		st.Finished == nil || st.Kind != "restart" {
		t.Errorf("the job %+v; want it failed with the launch error", st)
	}
	f.assertLockFree()
	if v := f.view(); v.Job != nil {
		t.Errorf("the view shows %+v; a failed job is not running", v.Job)
	}
}

// Everybody may follow a job.
func TestGetJob(t *testing.T) {
	f := newHarness(t)
	done := f.runJob(restartNginx)
	got := decode[jobs.State](t, f.send("GET", "/jobs/"+done.ID, nil, plain).want(t, http.StatusOK))
	if got.ID != done.ID || got.Status != jobs.Done || got.Kind != "restart" || got.Target != "nginx" ||
		got.Finished == nil {
		t.Errorf("state %+v", got)
	}
	f.send("GET", "/jobs/20261020T000000-0000", nil, plain).wantError(t, http.StatusNotFound, "not_found")
	for _, id := range []string{"x", "20261020T120000-ABCD", "..", "%2e%2e", "..%2f..%2fstate.json"} {
		f.send("GET", "/jobs/"+id, nil, plain).wantError(t, http.StatusNotFound, "not_found")
	}
}
