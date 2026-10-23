package api

import (
	"errors"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/jobs"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/logs"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/openrc"
)

// The failed starts of the view (failedStarts) and the kept scan of the system log (bootFailures).

// A stopped service is failed when the runner marked its last start as failed, or when it failed to start since the
// boot after its last good start or stop. No other state changes.
func TestViewFailedState(t *testing.T) {
	f := newHarness(t)
	got := map[string]openrc.State{}
	for _, s := range f.view().Services {
		got[s.Name] = s.State
	}
	for name, want := range map[string]openrc.State{
		"t-fail":   openrc.Failed,  // the failed marker
		"bootfail": openrc.Failed,  // a failure since the boot, no ok marker
		"bootlate": openrc.Failed,  // a failure after the ok marker
		"bootok":   openrc.Stopped, // the ok marker is newer than the failure
		"oldfail":  openrc.Stopped, // the failure is before the boot
		"marked":   openrc.Failed,  // rc-status says so
		"sshd":     openrc.Started, // a marker and a failure, but it runs
		"slow":     openrc.Starting,
		"chronyd":  openrc.Crashed,
		"docker":   openrc.Stopped,
	} {
		if got[name] != want {
			t.Errorf("%s is %q; want %q", name, got[name], want)
		}
	}
	if log := f.logged(); log != "" {
		t.Errorf("logged %q", log)
	}
}

// failedAt is the time the view gives a failed start, as RFC 3339 in UTC, "" when it gives none.
func failedAt(s ServiceView) string {
	if s.FailedAt == nil {
		return ""
	}
	return s.FailedAt.UTC().Format(time.RFC3339)
}

// A failed start comes with the time it failed (failedAt, RFC 3339): that of the failed marker or of the failure in
// the system log that made it failed, the later of the two. A failed state that OpenRC itself keeps, and every other
// state, has none.
func TestViewFailedAt(t *testing.T) {
	f := newHarness(t)
	f.mark("failed", "bootfail", at("07:30:00")) // older than its failure at the boot
	a := f.send("GET", "/view", nil, plain).want(t, http.StatusOK)
	s := byName(decode[ViewResponse](t, a))
	for name, want := range map[string]string{
		"t-fail":   "2026-10-20T07:40:00Z", // the failed marker
		"bootfail": "2026-10-20T07:46:19Z", // the failure in the system log, which is later than its marker
		"bootlate": "2026-10-20T07:55:00Z", // the failure after the ok marker
		"marked":   "",                     // rc-status says failed: OpenRC keeps no time
		"sshd":     "",                     // runs
		"bootok":   "",
		"docker":   "",
	} {
		if got := failedAt(s[name]); got != want {
			t.Errorf("%s (%s) failed at %q; want %q", name, s[name].State, got, want)
		}
	}
	if n := strings.Count(a.raw, `"failedAt"`); n != 3 || !strings.Contains(a.raw, `"failedAt":"2026-10-20T07:40:00Z"`) {
		t.Errorf("%d failedAt in the answer; want one for each of the three failed starts, in RFC 3339", n)
	}
	d := decode[Details](t, f.send("GET", "/services/t-fail", nil, plain).want(t, http.StatusOK))
	if got := failedAt(d.ServiceView); got != "2026-10-20T07:40:00Z" {
		t.Errorf("the details of t-fail: failed at %q; want the time of the view", got)
	}
}

// On a host without a real-time clock the failures of the boot are logged before NTP sets the clock: they count (the
// start line of syslogd decides), but their time is before the boot and tells nothing, so the view gives none, or the
// time of a failed marker.
func TestViewFailedAtWithoutClock(t *testing.T) {
	f := newHarness(t)
	f.write("/var/log/messages", `Jan  1 00:00:04 host syslog.info syslogd started: BusyBox v1.37.0
Jan  1 00:00:12 host daemon.err docker[300]: ERROR: docker failed to start
Jan  1 00:00:13 host daemon.err acpid[301]: ERROR: acpid failed to start
Oct 20 07:59:00 host daemon.info chronyd[400]: System clock wrong by 25000000 seconds
`)
	f.mark("failed", "acpid", at("07:45:00"))
	s := byName(f.view())
	for name, want := range map[string]string{"docker": "", "acpid": "2026-10-20T07:45:00Z"} {
		if s[name].State != openrc.Failed || failedAt(s[name]) != want {
			t.Errorf("%s is %s, failed at %q; want failed at %q", name, s[name].State, failedAt(s[name]), want)
		}
	}
}

// Markers that cannot be read mean no markers, and a boot time that cannot be read means no failures since the boot:
// the page shows the rest, and the reason is logged.
func TestViewSurvivesMarkersAndBootTime(t *testing.T) {
	t.Run("markers", func(t *testing.T) {
		f := newHarness(t)
		f.remove(jobs.DefaultMarkDir + "/failed")
		f.write(jobs.DefaultMarkDir+"/failed", "not a directory")
		s := byName(f.view())
		if s["t-fail"].State != openrc.Stopped || !s["t-fail"].AutostartIdle {
			t.Errorf("t-fail %+v; want stopped without its marker", s["t-fail"])
		}
		if s["bootok"].State != openrc.Failed { // its ok marker is not read either
			t.Errorf("bootok %+v; want failed by the system log alone", s["bootok"])
		}
		if !strings.Contains(f.logged(), "markers") {
			t.Errorf("logged %q; want the markers", f.logged())
		}
	})
	t.Run("boot time", func(t *testing.T) {
		f := newHarness(t)
		f.remove("/proc/uptime")
		s := byName(f.view())
		if s["bootfail"].State != openrc.Stopped || s["bootlate"].State != openrc.Stopped ||
			s["t-fail"].State != openrc.Failed {
			t.Errorf("bootfail %q, bootlate %q, t-fail %q; want only the marker", s["bootfail"].State, s["bootlate"].State,
				s["t-fail"].State)
		}
		if !strings.Contains(f.logged(), "boot") {
			t.Errorf("logged %q; want the boot time", f.logged())
		}
	})
}

// countScans makes the scans of the system log for the failed starts since the boot countable, and fail with fail
// when it is set.
func (f *harness) countScans(fail error) *atomic.Int32 {
	var n atomic.Int32
	f.api.scanBoot = func(root string, boot, now time.Time) (map[string]time.Time, bool, error) {
		n.Add(1)
		if fail != nil {
			return nil, false, fail
		}
		return logs.BootFailures(root, boot, now)
	}
	return &n
}

// mtime is the time the file p of the test host was changed; touch sets it.
func (f *harness) mtime(p string) time.Time {
	f.t.Helper()
	st, err := os.Stat(f.path(p))
	if err != nil {
		f.t.Fatal(err)
	}
	return st.ModTime()
}

func (f *harness) touch(p string, mtime time.Time) {
	f.t.Helper()
	if err := os.Chtimes(f.path(p), mtime, mtime); err != nil {
		f.t.Fatal(err)
	}
}

// The view is asked for every 5 s: the failed starts since the boot are scanned once while the system log stays as it
// is. A change of the size, the time of change or the inode of either file of the log scans anew (each case changes one
// of them alone, whatever the granularity of the file times). The boot time does not count: the start line of busybox
// syslogd tells which lines are of this boot.
func TestViewCachesBootFailures(t *testing.T) {
	const messages, rotated = "/var/log/messages", "/var/log/messages.0"
	f := newHarness(t)
	scans := f.countScans(nil)
	first := byName(f.view())
	if second := byName(f.view()); scans.Load() != 1 || !reflect.DeepEqual(second, first) {
		t.Fatalf("%d scans for two views (the same answer: %v); want one", scans.Load(), reflect.DeepEqual(second, first))
	}
	for _, c := range []struct {
		what   string
		change func()
		scans  int32
	}{
		{"a line added (the size)", func() {
			mtime := f.mtime(messages)
			f.appendTo(messages, "Oct 20 07:58:00 host daemon.err docker[9]: ERROR: docker failed to start\n")
			f.touch(messages, mtime)
		}, 2},
		{"the uptime a moment later", func() { f.write("/proc/uptime", "3600.30 7000.00\n") }, 2},
		{"rewritten to the same size (the time of change)", func() {
			mtime := f.mtime(messages)
			data, err := os.ReadFile(f.path(messages))
			if err != nil {
				t.Fatal(err)
			}
			f.write(messages, strings.Replace(string(data), "docker[9]", "docker[8]", 1))
			f.touch(messages, mtime.Add(time.Second))
		}, 3},
		{"the log rotated (a file that was not there)", func() { f.write(rotated, "") }, 4},
		{"the rotated log replaced by another file (the inode)", func() {
			mtime := f.mtime(rotated)
			f.write(rotated+".new", "")
			f.touch(rotated+".new", mtime)
			if err := os.Rename(f.path(rotated+".new"), f.path(rotated)); err != nil {
				t.Fatal(err)
			}
		}, 5},
		{"another boot time (NTP set the clock)", func() { f.write("/proc/uptime", "600.00 7000.00\n") }, 5},
	} {
		c.change()
		s := byName(f.view())
		f.view()
		if scans.Load() != c.scans {
			t.Errorf("%s: %d scans; want %d", c.what, scans.Load(), c.scans)
		}
		if s["docker"].State != openrc.Failed || s["bootfail"].State != openrc.Failed {
			t.Errorf("%s: docker %q, bootfail %q; want both failed", c.what, s["docker"].State, s["bootfail"].State)
		}
	}
}

// Without the start line of syslogd the boot time decides which failures count, so another boot time scans anew; the
// uptime read a moment later does not, also when the boot time it gives crosses a half second.
func TestViewCachesByBootTime(t *testing.T) {
	f := newHarness(t)
	f.write("/var/log/messages", strings.Replace(syslog, "Oct 20 07:00:05 host syslog.info syslogd started: BusyBox v1.37.0\n", "", 1))
	scans := f.countScans(nil)
	f.write("/proc/uptime", "3600.49 7000.00\n") // booted at 06:59:59.51
	if s := byName(f.view()); s["bootfail"].State != openrc.Failed || s["oldfail"].State != openrc.Stopped {
		t.Errorf("bootfail %q, oldfail %q; want the failure after the boot time only", s["bootfail"].State,
			s["oldfail"].State)
	}
	f.write("/proc/uptime", "3600.51 7000.00\n") // 06:59:59.49
	f.view()
	if scans.Load() != 1 {
		t.Errorf("%d scans after the uptime a moment later; want 1", scans.Load())
	}
	f.write("/proc/uptime", "600.00 7000.00\n") // 07:50:00: bootfail failed before, bootlate after
	s := byName(f.view())
	if scans.Load() != 2 {
		t.Errorf("%d scans after another boot time; want 2", scans.Load())
	}
	if s["bootfail"].State != openrc.Stopped || s["bootlate"].State != openrc.Failed {
		t.Errorf("after another boot time bootfail %q, bootlate %q; want stopped and failed", s["bootfail"].State,
			s["bootlate"].State)
	}
}

// A scan that fails is not kept: the next view scans again.
func TestViewDoesNotCacheAFailedScan(t *testing.T) {
	f := newHarness(t)
	scans := f.countScans(errors.New("read /var/log/messages: input/output error"))
	f.view()
	f.view()
	if scans.Load() != 2 {
		t.Errorf("%d scans; want one for each view", scans.Load())
	}
	if !strings.Contains(f.logged(), "input/output error") {
		t.Errorf("logged %q; want the reason", f.logged())
	}
}
