package openrc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/run"
)

func TestParseRcStatus(t *testing.T) {
	for _, tc := range []struct {
		file string
		want map[string]listed
	}{
		// t-daemon is listed twice (default and needed/wanted); t-fail, whose start failed, is stopped: OpenRC does not
		// mark a failed start.
		{"rc-status.txt", map[string]listed{"t-oneshot": {Started, ""}, "t-daemon": {Started, ""}, "t-fail": {Stopped, ""},
			"t-crash": {Crashed, ""}, "t-log": {Started, ""}, "t-super": {Started, "00:00:02"}, "t-needs": {Started, ""}}},
		{"rc-status-failed.txt", map[string]listed{"t-daemon": {Started, ""}, "t-fail": {Stopped, ""}}},
		{"rc-status-states.txt", map[string]listed{"t-slow": {Starting, ""}, "t-inactive": {Inactive, ""},
			"t-markfailed": {Failed, ""}}},
	} {
		out := readTestdata(t, tc.file)
		if got := parseRcStatus(out); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s:\n got %v\nwant %v", tc.file, got, tc.want)
		}
		// In a container every start and stop prints a line about the read-only cgroup, also amid the output.
		noise := "mkdir: can't create directory '/sys/fs/cgroup/openrc.t-daemon': Read-only file system\n"
		noisy := noise + strings.ReplaceAll(out, "Dynamic Runlevel: manual\n", "Dynamic Runlevel: manual\n"+noise)
		if got := parseRcStatus(noisy); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s with the cgroup lines:\n got %v\nwant %v", tc.file, got, tc.want)
		}
	}

	long := strings.Repeat("x", 64)
	out := "Runlevel: default\n" +
		" t-stop                                                            [ stopping  ]\n" +
		" " + long + " [  started 00:00:00 (0) ]\n" + // the longest name: one space, no wrapping
		" t-days                                      [  started 1 day(s) 02:03:04 (12) ]\n" + // the uptime is opaque
		" t-sched                                                           [ scheduled ]\n" + // not a state of the view
		" * Caching service dependencies ...                                [ ok ]\n" +
		"Service 'hwdrivers' needs non existent service 'dev'\n" +
		"\n"
	want := map[string]listed{"t-stop": {Stopping, ""}, long: {Started, "00:00:00"}, "t-days": {Started, "1 day(s) 02:03:04"}}
	if got := parseRcStatus(out); !reflect.DeepEqual(got, want) {
		t.Errorf("other forms:\n got %v\nwant %v", got, want)
	}
}

func TestReadCapture(t *testing.T) {
	r, fake := fromCapture(t, "rc-status.txt")
	snap, err := r.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []Service{
		// Its daemon was killed: the pid file names a dead process, so there is no PID.
		{Name: "t-crash", Description: "Test daemon that the capture kills", State: Crashed, Runlevels: []string{}},
		{Name: "t-daemon", Description: "Test daemon: sleeps in the background", State: Started, Runlevels: []string{"default"},
			PID: 160},
		// Its start failed; /run/openrc/failed is empty and rc-status says stopped.
		{Name: "t-fail", Description: "Test service whose start fails", State: Stopped, Runlevels: []string{"default"}},
		// The pid files of t-log and t-needs are not in the capture.
		{Name: "t-log", Description: "Test daemon that writes output_log and error_log", State: Started, Runlevels: []string{}},
		{Name: "t-needs", Description: "Test daemon that needs t-daemon", State: Started, Runlevels: []string{}},
		{Name: "t-oneshot", Description: "Test one-shot service without a daemon", State: Started, OneShot: true,
			Runlevels: []string{"boot"}},
		// The PID is supervise-daemon's child, not the supervisor.
		{Name: "t-super", Description: "Test daemon under supervise-daemon", State: Started, Supervised: true,
			Runlevels: []string{}, PID: 187, Uptime: "00:00:02"},
	}
	for i := range want {
		want[i].Script = "/etc/init.d/" + want[i].Name
	}
	if !snap.Present || !reflect.DeepEqual(snap.Services, want) {
		t.Errorf("Read:\n got %+v\nwant %+v", snap, want)
	}
	if got := fake.Commands(); !reflect.DeepEqual(got, []string{"/bin/rc-status --all --nocolor"}) {
		t.Errorf("commands %q, want rc-status only", got)
	} else if fake.Calls[0].Cmd.Timeout != 30*time.Second {
		t.Errorf("rc-status timeout %v, want 30s", fake.Calls[0].Cmd.Timeout)
	}

	// Without root rc-status cannot see that a daemon of root is gone and still says started: the reader looks in /proc.
	// t-daemon's process ends, and so does t-super's supervisor (its child still runs).
	for _, pid := range []string{"160", "186"} {
		if err := os.Remove(filepath.Join(r.Root, "proc", pid)); err != nil {
			t.Fatal(err)
		}
	}
	if snap, err = r.Read(ctx); err != nil {
		t.Fatal(err)
	}
	want[1].State, want[1].PID = Crashed, 0
	want[6].State, want[6].PID, want[6].Uptime = Crashed, 0, "" // rc-status prints no uptime for a crashed service
	if !reflect.DeepEqual(snap.Services, want) {
		t.Errorf("Read after the daemons ended:\n got %+v\nwant %+v", snap.Services, want)
	}
}

func TestReadLiveness(t *testing.T) {
	f := newFixture(t)
	f.openrc()
	f.link("/var/run", "../run") // as on Alpine: the daemon entry of a supervised service names /var/run/supervise-<s>.pid
	f.dir("/proc/1")             // init is visible: /proc tells which processes run
	status := ""
	daemon := func(name, state, pidfile, pid string) {
		f.service(name, name)
		status += " " + name + "                [  " + state + "  ]\n"
		f.file(runDir+"/daemons/"+name+"/001", "exec=/usr/sbin/"+name+"\npidfile="+pidfile+"\n", 0o644)
		if pid != "" {
			f.file(pidfile, pid, 0o644)
		}
	}
	proc := func(pids ...string) {
		for _, pid := range pids {
			f.dir("/proc/" + pid)
		}
	}
	daemon("alive", "started", "/run/alive.pid", "100\n")
	proc("100")
	daemon("gone", "started", "/run/gone.pid", "101\n") // no /proc/101: crashed
	daemon("nofile", "started", "/run/nofile.pid", "")  // the pid file is missing: unknown
	daemon("garbage", "started", "/run/garbage.pid", "abc\n")
	daemon("zero", "started", "/run/zero.pid", "0\n")
	daemon("multi", "started", "/run/multi-1.pid", "103\n") // one daemon of two is gone: crashed, as OpenRC says
	f.file(runDir+"/daemons/multi/002", "exec=/usr/sbin/multi-helper\npidfile=/run/multi-2.pid\n", 0o644)
	f.file("/run/multi-2.pid", "104\n", 0o644)
	proc("103")
	daemon("listed", "crashed", "/run/listed.pid", "105\n")
	daemon("stopped", "stopped", "/run/stopped.pid", "106\n") // stale files of a stopped service change nothing
	// Supervised: the daemon entry names the supervisor's pid file (what OpenRC checks), the PID is the child's.
	supervised := func(name, supervisor, child string) {
		daemon(name, "started", "/var/run/supervise-"+name+".pid", supervisor+"\n")
		f.file(runDir+"/options/"+name+"/child_pid", child, 0o644)
		f.file(runDir+"/options/"+name+"/start_time", "2026-10-20 07:46:19", 0o644)
	}
	supervised("sup", "200", "201")
	proc("200", "201")
	supervised("supdead", "210", "211") // the supervisor is gone: crashed, also while its child runs
	proc("211")
	supervised("respawning", "220", "221") // the child is gone, the supervisor starts it again: started, no PID
	proc("220")
	unreadable := os.Geteuid() != 0 // root reads a file of mode 0000
	if unreadable {
		daemon("locked", "started", "/run/locked.pid", "107\n") // cannot be read: unknown, not crashed
		f.lock("/run/locked.pid")
	}
	r, _ := f.reader(status)
	snap, err := r.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	type facts struct {
		state State
		pid   int
	}
	want := map[string]facts{"alive": {Started, 100}, "gone": {Crashed, 0}, "nofile": {Started, 0},
		"garbage": {Started, 0}, "zero": {Started, 0}, "multi": {Crashed, 0}, "listed": {Crashed, 0},
		"stopped": {Stopped, 0}, "sup": {Started, 201}, "supdead": {Crashed, 0}, "respawning": {Started, 0}}
	if unreadable {
		want["locked"] = facts{Started, 0}
	}
	got := map[string]facts{}
	for _, s := range snap.Services {
		got[s.Name] = facts{s.State, s.PID}
		if s.OneShot {
			t.Errorf("%s: OneShot, but it has a daemon", s.Name)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("state and pid\n got %+v\nwant %+v", got, want)
	}

	// Where /proc does not show init (no /proc, or hidepid hides the processes of other users) it cannot tell: the
	// state stays what rc-status said, with the PID of the pid file.
	if err := os.Remove(f.path("/proc/1")); err != nil {
		t.Fatal(err)
	}
	if snap, err = r.Read(ctx); err != nil {
		t.Fatal(err)
	}
	for _, s := range snap.Services {
		if s.Name == "gone" && (s.State != Started || s.PID != 101) {
			t.Errorf("gone without a visible /proc: %s, PID %d, want started, PID 101", s.State, s.PID)
		}
	}
}

func TestReadUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a directory of mode 0000")
	}
	// Only a missing entry means "none": a directory or file of /run/openrc or /etc/runlevels that cannot be read fails
	// the Read, instead of making a daemon disappear (a started service would turn one-shot without a PID).
	for _, locked := range []string{runDir + "/daemons/web", runDir + "/daemons/web/001", runDir + "/options/web",
		runDir + "/started", runlevelDir, runlevelDir + "/default", runDir} {
		f := newFixture(t)
		f.openrc()
		f.service("web", "web", "default")
		f.file(runDir+"/daemons/web/001", "exec=/usr/sbin/web\npidfile=/run/web.pid\n", 0o644)
		f.dir(runDir + "/options/web")
		f.service("late", "late") // not listed by rc-status: its state comes from /run/openrc/started
		f.mark("started", "late")
		r, _ := f.reader(" web                [  started  ]\n")
		if snap, err := r.Read(ctx); err != nil || len(snap.Services) != 2 || snap.Services[1].OneShot {
			t.Fatalf("%s readable: Read = %+v, %v", locked, snap, err)
		}
		f.lock(locked)
		if snap, err := r.Read(ctx); err == nil || !reflect.DeepEqual(snap, Snapshot{}) {
			t.Errorf("%s unreadable: Read = %+v, %v, want an error", locked, snap, err)
		}
		if locked == runDir+"/daemons/web" {
			if got := r.Programs("web"); !reflect.DeepEqual(got, []string{"web"}) {
				t.Errorf("Programs with %s unreadable = %q, want the name alone", locked, got)
			}
		}
	}
}

func TestReadStates(t *testing.T) {
	f := newFixture(t)
	f.openrc()
	for _, name := range []string{"t-slow", "t-inactive", "t-markfailed", "late", "coming", "going", "idle", "broke", "plain"} {
		f.service(name, name)
	}
	// rc-status wins over the state directories of /run/openrc.
	f.mark("started", "t-slow")
	f.mark("failed", "t-markfailed")
	// Services rc-status does not list (it ran a moment earlier) get the state of their directory; a failed marker
	// alone is no state of its own here, only rc-status says failed.
	f.mark("started", "late")
	f.mark("starting", "coming")
	f.mark("stopping", "going")
	f.mark("inactive", "idle")
	f.mark("failed", "broke")
	r, _ := f.reader(readTestdata(t, "rc-status-states.txt"))
	snap, err := r.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]State{"t-slow": Starting, "t-inactive": Inactive, "t-markfailed": Failed, "late": Started,
		"coming": Starting, "going": Stopping, "idle": Inactive, "broke": Stopped, "plain": Stopped}
	got := map[string]State{}
	for _, s := range snap.Services {
		got[s.Name] = s.State
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("states\n got %v\nwant %v", got, want)
	}
}

// The pid files and options of the daemons, in a root without /proc: whether a process runs is unknown here
// (TestReadLiveness has /proc).
func TestReadDaemons(t *testing.T) {
	f := newFixture(t)
	f.openrc()
	daemon := func(name, n, content string) { f.file(runDir+"/daemons/"+name+"/"+n, content, 0o644) }
	status := ""
	for _, name := range []string{"web", "multi", "junk", "fifo", "nodaemon", "sup", "childonly", "timeonly", "dead"} {
		f.service(name, name)
		state := "started"
		if name == "dead" {
			state = "crashed"
		}
		status += " " + name + "                [  " + state + "  ]\n"
	}
	daemon("web", "001", "exec=/usr/sbin/web\nargv_0=/usr/sbin/web\npidfile=/run/web.pid\n")
	f.file("/run/web.pid", "1234\n", 0o644)
	daemon("multi", "001", "exec=/bin/a\npidfile=/run/missing.pid\n")
	daemon("multi", "002", "exec=/bin/b\npidfile=/run/multi.pid\n")
	f.file("/run/multi.pid", "77", 0o644)
	daemon("junk", "001", "exec=/bin/junk\npidfile=/run/junk.pid\n")
	f.file("/run/junk.pid", "abc\n", 0o644)
	daemon("fifo", "001", "exec=/bin/fifo\npidfile=/run/fifo.pid\n")
	if err := syscall.Mkfifo(f.path("/run/fifo.pid"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.dir(runDir + "/daemons/nodaemon") // the directory without a daemon file: no daemon either
	daemon("sup", "001", "exec=/bin/sleep\npidfile=/var/run/supervise-sup.pid\n")
	f.file(runDir+"/options/sup/child_pid", "555", 0o644)
	f.file(runDir+"/options/sup/start_time", "2026-10-20 07:46:19", 0o644)
	// Either file of supervise-daemon makes a service supervised, and so no one-shot, also without a daemon file.
	f.file(runDir+"/options/childonly/child_pid", "556", 0o644)
	f.file(runDir+"/options/timeonly/start_time", "2026-10-20 07:46:19", 0o644)
	daemon("dead", "001", "exec=/bin/dead\npidfile=/run/dead.pid\n")
	f.file("/run/dead.pid", "999\n", 0o644)
	r, _ := f.reader(status)
	snap, err := r.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	type facts struct {
		pid                 int
		oneShot, supervised bool
	}
	want := map[string]facts{"web": {1234, false, false}, "multi": {77, false, false}, "junk": {0, false, false},
		"fifo": {0, false, false}, "nodaemon": {0, true, false}, "sup": {555, false, true}, "childonly": {556, false, true},
		"timeonly": {0, false, true}, "dead": {0, false, false}}
	got := map[string]facts{}
	for _, s := range snap.Services {
		got[s.Name] = facts{s.PID, s.OneShot, s.Supervised}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("pid, one-shot, supervised\n got %+v\nwant %+v", got, want)
	}
}

func TestReadWithoutOpenRC(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(f *fixture)
	}{
		{"nothing", func(f *fixture) {}},
		{"tools without softlevel", func(f *fixture) { f.tools() }}, // installed, but OpenRC did not run
		{"softlevel without rc-service", func(f *fixture) {
			f.file(runDir+"/softlevel", "default", 0o644)
			f.file("/bin/rc-status", "", 0o755)
		}},
	} {
		f := newFixture(t)
		f.service("sshd", "OpenSSH", "default")
		tc.setup(f)
		r, fake := f.reader("")
		snap, err := r.Read(ctx)
		if err != nil || !reflect.DeepEqual(snap, Snapshot{}) {
			t.Errorf("%s: Read = %+v, %v, want Snapshot{Present: false} and no error", tc.name, snap, err)
		}
		if got := fake.Commands(); len(got) != 0 {
			t.Errorf("%s: commands %q, want none", tc.name, got)
		}
	}
}

func TestReadErrors(t *testing.T) {
	f := newFixture(t)
	f.openrc()
	f.service("sshd", "OpenSSH")
	r, fake := f.reader("")
	fake.Results[r.Tools.status().String()] = run.FakeResult{Output: "rc-status: oops\n", Status: 1}
	if snap, err := r.Read(ctx); err == nil || !strings.Contains(err.Error(), "oops") || !reflect.DeepEqual(snap, Snapshot{}) {
		t.Errorf("rc-status fails: Read = %+v, %v, want an error", snap, err)
	}

	r.Tools.RcStatus = ""
	if snap, err := r.Read(ctx); err == nil || !reflect.DeepEqual(snap, Snapshot{}) {
		t.Errorf("no rc-status: Read = %+v, %v, want an error", snap, err)
	}

	f = newFixture(t)
	f.openrc() // and no /etc/init.d
	r, _ = f.reader("")
	if snap, err := r.Read(ctx); err == nil || !reflect.DeepEqual(snap, Snapshot{}) {
		t.Errorf("no /etc/init.d: Read = %+v, %v, want an error", snap, err)
	}
}

func TestPrograms(t *testing.T) {
	r, _ := fromCapture(t, "rc-status.txt")
	for name, want := range map[string][]string{
		"t-log":     {"sh", "t-log"},
		"t-daemon":  {"sleep", "t-daemon"},
		"t-super":   {"sleep", "t-super"}, // supervise-daemon's own lines name it only in the message
		"t-oneshot": {"t-oneshot"},
		"t-fail":    {"t-fail"},
	} {
		if got := r.Programs(name); !reflect.DeepEqual(got, want) {
			t.Errorf("Programs(%q) = %q, want %q", name, got, want)
		}
	}

	f := newFixture(t)
	for n, exec := range map[string]string{"001": "/usr/bin/dockerd", "002": "/usr/bin/containerd", "003": "/usr/bin/dockerd"} {
		f.file(runDir+"/daemons/docker/"+n, "exec="+exec+"\npidfile=/run/docker-"+n+".pid\n", 0o644)
	}
	f.file(runDir+"/daemons/docker/004", "pidfile=/run/other.pid\n", 0o644)
	r = Reader{Root: f.root}
	if got, want := r.Programs("docker"), []string{"containerd", "docker", "dockerd"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Programs(docker) = %q, want %q", got, want)
	}
	if got := r.Programs("../docker"); got != nil {
		t.Errorf("Programs of an invalid name = %q, want nil", got)
	}
}

func TestServiceJSON(t *testing.T) {
	s := Service{Name: "sshd", Description: "OpenSSH server daemon", State: Started, Supervised: true,
		Runlevels: []string{"default"}, PID: 42, Uptime: "00:00:02", Script: "/etc/init.d/sshd"}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"name":"sshd","description":"OpenSSH server daemon","state":"started","oneShot":false,"supervised":true,` +
		`"runlevels":["default"],"pid":42,"uptime":"00:00:02","script":"/etc/init.d/sshd"}`
	if string(b) != want {
		t.Errorf("JSON\n got %s\nwant %s", b, want)
	}
	b, _ = json.Marshal(Service{Name: "local", State: Stopped, Runlevels: []string{}, Script: "/etc/init.d/local"})
	want = `{"name":"local","description":"","state":"stopped","oneShot":false,"supervised":false,"runlevels":[],` +
		`"script":"/etc/init.d/local"}`
	if string(b) != want {
		t.Errorf("JSON without pid and uptime\n got %s\nwant %s", b, want)
	}
}
