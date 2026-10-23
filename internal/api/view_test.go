package api

import (
	"encoding/json"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/jobs"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/openrc"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/policy"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/run"
)

// view asks for /view as a user and returns the decoded answer.
func (f *harness) view() ViewResponse {
	f.t.Helper()
	return decode[ViewResponse](f.t, f.send("GET", "/view", nil, plain).want(f.t, http.StatusOK))
}

// byName are the services of the view by name.
func byName(v ViewResponse) map[string]ServiceView {
	out := map[string]ServiceView{}
	for _, s := range v.Services {
		out[s.Name] = s
	}
	return out
}

// keys are the keys of a JSON object, sorted.
func keys(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return slices.Sorted(maps.Keys(m))
}

// Everybody sees the services, in camelCase, with their class and what the policy says to each action.
func TestViewForEverybody(t *testing.T) {
	f := newHarness(t)
	a := f.send("GET", "/view", nil, plain).want(t, http.StatusOK)
	v := decode[ViewResponse](t, a)
	if !v.Present || v.Mode != "sys" {
		t.Fatalf("present %v, mode %q; want OpenRC on a sys host", v.Present, v.Mode)
	}
	var names []string
	for _, s := range v.Services {
		names = append(names, s.Name)
	}
	want := []string{"acpid", "bootfail", "bootlate", "bootok", "chronyd", "cron", "devfs", "docker", "edelweiss",
		"marked", "net.eth0", "net.lo", "networking", "nginx", "oldfail", "proxy", "slow", "sshd", "t-fail"}
	if !slices.Equal(names, want) {
		t.Fatalf("services %v; want %v", names, want)
	}
	s := byName(v)
	nginx := s["nginx"]
	if nginx.Description != "The nginx service" || nginx.State != openrc.Started || nginx.PID != 4242 || nginx.OneShot ||
		nginx.Script != "/etc/init.d/nginx" || !slices.Equal(nginx.Runlevels, []string{"default"}) ||
		nginx.Class != policy.Free {
		t.Errorf("nginx %+v", nginx)
	}
	for name, want := range map[string]map[policy.Action]policy.Verdict{
		"edelweiss": {policy.Start: policy.Deny, policy.Restart: policy.Allow, policy.Stop: policy.Deny,
			policy.Disable: policy.Deny, policy.Enable: policy.Deny},
		"sshd": {policy.Start: policy.Allow, policy.Restart: policy.Warn, policy.Stop: policy.Warn,
			policy.Disable: policy.Warn, policy.Enable: policy.Allow},
		"devfs": {policy.Start: policy.Deny, policy.Restart: policy.Deny, policy.Stop: policy.Deny,
			policy.Disable: policy.Deny, policy.Enable: policy.Deny},
		"cron": {policy.Start: policy.Allow, policy.Restart: policy.Allow, policy.Stop: policy.Allow,
			policy.Disable: policy.Deny, policy.Enable: policy.Allow}, // in no runlevel: nothing to switch off
	} {
		if got := s[name].Allowed; !reflect.DeepEqual(got, want) {
			t.Errorf("%s allowed %v; want %v", name, got, want)
		}
	}
	for name, want := range map[string]policy.Class{"edelweiss": policy.Edelweiss, "devfs": policy.Base,
		"sshd": policy.Remote, "networking": policy.Network, "net.eth0": policy.Network, "docker": policy.Free} {
		if got := s[name].Class; got != want {
			t.Errorf("%s class %q; want %q", name, got, want)
		}
	}

	// the JSON: camelCase, and no job while none runs
	var top struct {
		Services []json.RawMessage `json:"services"`
		Counts   json.RawMessage   `json:"counts"`
	}
	if err := json.Unmarshal([]byte(a.raw), &top); err != nil {
		t.Fatal(err)
	}
	if got := keys(t, json.RawMessage(a.raw)); !slices.Equal(got, []string{"counts", "mode", "present", "services"}) {
		t.Errorf("keys of the view %v", got)
	}
	if got := keys(t, top.Counts); !slices.Equal(got, []string{"problems", "running", "total"}) {
		t.Errorf("keys of the counts %v", got)
	}
	wantKeys := []string{"allowed", "autostartIdle", "class", "description", "name", "oneShot", "pid", "runlevels",
		"script", "state", "supervised"}
	if got := keys(t, top.Services[slices.Index(names, "nginx")]); !slices.Equal(got, wantKeys) {
		t.Errorf("keys of a service %v; want %v", got, wantKeys)
	}
	const byAction = `"allowed":{"disable":"allow","enable":"allow","restart":"allow","start":"allow","stop":"allow"}`
	if !strings.Contains(a.raw, byAction) {
		t.Errorf("allowed of nginx is not by action: %s", top.Services[slices.Index(names, "nginx")])
	}
}

// Without OpenRC the view says so, with no services and no error.
func TestViewWithoutOpenRC(t *testing.T) {
	for _, c := range []struct{ name, remove string }{
		{"no softlevel", "/run/openrc/softlevel"},
		{"no rc-service", rcService},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newHarness(t)
			f.remove(c.remove)
			a := f.send("GET", "/view", nil, plain).want(t, http.StatusOK)
			v := decode[ViewResponse](t, a)
			if v.Present || len(v.Services) != 0 || v.Counts != (Counts{}) || v.Mode != "sys" {
				t.Errorf("view %+v; want no OpenRC", v)
			}
			if !strings.Contains(a.raw, `"services":[]`) || !strings.Contains(a.raw, `"present":false`) {
				t.Errorf("answer %s; want present false and an empty list", a.raw)
			}
			if n := f.ran(rcStatus); n != 0 {
				t.Errorf("rc-status ran %d times without OpenRC", n)
			}
		})
	}
}

// The head counts the services, those that run (started, one-shot ones too) and the problems (crashed or failed, also
// when the failure was derived); autostartIdle are the stopped services of default or boot.
func TestViewCountsAndIdle(t *testing.T) {
	f := newHarness(t)
	v := f.view()
	if want := (Counts{Total: 19, Running: 6, Problems: 5}); v.Counts != want {
		t.Errorf("counts %+v; want %+v", v.Counts, want)
	}
	var idle []string
	for _, s := range v.Services {
		if s.AutostartIdle {
			idle = append(idle, s.Name)
		}
	}
	// not chronyd (crashed), t-fail, bootfail, bootlate (failed), slow (starting), cron and net.lo (in no runlevel)
	if want := []string{"acpid", "bootok", "docker", "oldfail"}; !slices.Equal(idle, want) {
		t.Errorf("idle %v; want %v", idle, want)
	}
	if s := byName(v)["networking"]; !s.OneShot || s.State != openrc.Started {
		t.Errorf("networking %+v; want a one-shot service that counts as running", s)
	}
}

// A mode that cannot be read is no mode in the view, which shows the services all the same.
func TestViewModeUnreadable(t *testing.T) {
	f := newHarness(t)
	f.remove("/proc/mounts")
	if v := f.view(); v.Mode != "" || len(v.Services) != 19 {
		t.Errorf("mode %q, %d services; want no mode and the services", v.Mode, len(v.Services))
	}
	if !strings.Contains(f.logged(), "mode") {
		t.Errorf("logged %q; want the mode", f.logged())
	}
}

// A failing rc-status is a failure of the page.
func TestViewHostUnreadable(t *testing.T) {
	f := newHarness(t)
	f.host.Results[rcStatus+" --all --nocolor"] = run.FakeResult{Output: "rc-status: broken", Status: 1}
	f.send("GET", "/view", nil, plain).wantError(t, http.StatusInternalServerError, "failed")
}

// The view shows the newest job while it runs, and when its runner was lost (interrupted); an ended one is not shown.
func TestViewShowsRunningAndInterruptedJob(t *testing.T) {
	f := newHarness(t)
	lock, err := f.api.Jobs.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	_, created, err := f.api.Jobs.Create(lock, jobs.Spec{Kind: "restart", Target: "nginx"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if v := f.view(); v.Job == nil || v.Job.ID != created.ID || v.Job.Status != jobs.Running {
		t.Fatalf("job %+v; want the running one", v.Job)
	}
	lock.Close() // the runner is gone
	if v := f.view(); v.Job == nil || v.Job.ID != created.ID || v.Job.Status != jobs.Interrupted {
		t.Fatalf("job %+v; want the interrupted one", v.Job)
	}

	f2 := newHarness(t)
	done := f2.runJob(restartNginx)
	if done.Status != jobs.Done {
		t.Fatalf("job %+v", done)
	}
	if a := f2.send("GET", "/view", nil, plain).want(t, http.StatusOK); strings.Contains(a.raw, `"job"`) {
		t.Errorf("the view shows an ended job: %s", a.raw)
	}
}

// The details are the service as the view shows it and its relations.
func TestDetails(t *testing.T) {
	f := newHarness(t)
	a := f.send("GET", "/services/nginx", nil, plain).want(t, http.StatusOK)
	d := decode[Details](t, a)
	if d.Name != "nginx" || d.State != openrc.Started || d.PID != 4242 || d.Class != policy.Free ||
		d.Allowed[policy.Stop] != policy.Allow || d.Description != "The nginx service" {
		t.Errorf("details %+v", d)
	}
	if !slices.Equal(d.Needs, []string{"localmount", "net"}) || !slices.Equal(d.Uses, []string{"logger"}) ||
		!slices.Equal(d.After, []string{"firewall"}) || d.NeededBy == nil || len(d.NeededBy) != 0 {
		t.Errorf("relations needs %v, uses %v, after %v, neededBy %v", d.Needs, d.Uses, d.After, d.NeededBy)
	}
	for _, q := range []string{"ineed", "iuse", "iafter", "needsme"} {
		if n := f.ran(rcService + " nginx " + q); n != 1 {
			t.Errorf("rc-service nginx %s ran %d times", q, n)
		}
	}
	if !strings.Contains(a.raw, `"neededBy":[]`) || !strings.Contains(a.raw, `"autostartIdle":false`) {
		t.Errorf("answer %s", a.raw)
	}
	// the state is the one of the view
	d = decode[Details](t, f.send("GET", "/services/t-fail", nil, plain).want(t, http.StatusOK))
	if d.State != openrc.Failed {
		t.Errorf("t-fail is %q; want failed, as in the view", d.State)
	}
	// a relation that cannot be read is a failure
	f.host.Results[rcService+" docker iuse"] = run.FakeResult{Status: 1}
	f.send("GET", "/services/docker", nil, plain).wantError(t, http.StatusInternalServerError, "failed")
}
