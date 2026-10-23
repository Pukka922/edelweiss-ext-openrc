package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"golang.org/x/sys/unix"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/jobs"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/openrc"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/run"
	"github.com/Pukka922/edelweiss/sdk"
	"github.com/Pukka922/edelweiss/sdk/sdktest"
)

var admin = sdk.User{Name: "myUser", UID: 1001, Admin: true, Lang: "en"}
var plain = sdk.User{Name: "myUser", UID: 1001, Admin: false, Lang: "en"}

// now is the time of the API in the tests. The host booted an hour before (proc/uptime), at 07:00.
var now = time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)

// at is a time of the test day, as "07:50:00".
func at(clock string) time.Time {
	t, err := time.Parse(time.DateTime, "2026-10-20 "+clock)
	if err != nil {
		panic(err)
	}
	return t
}

// The tools, where Alpine has them.
const (
	rcService = "/sbin/rc-service"
	rcUpdate  = "/sbin/rc-update"
	rcStatus  = "/bin/rc-status"
	lbu       = "/sbin/lbu"
)

// service is a service of the test host: the state rc-status prints for it ("" when it does not list it) and its
// runlevels.
type service struct {
	name, state string
	runlevels   []string
}

// services are the services of the test host besides net.lo and net.eth0 (a link to net.lo), which are in no runlevel
// and stopped. The comments tell what the markers and the system log of the host make of them.
var services = []service{
	{"acpid", "stopped", []string{"boot"}},        // in the autostart of boot, not running: idle
	{"bootfail", "stopped", []string{"boot"}},     // failed to start at the boot, no marker since: failed
	{"bootlate", "stopped", []string{"default"}},  // failed to start at the boot after its ok marker: failed
	{"bootok", "stopped", []string{"default"}},    // failed at the boot, then started well (ok marker): stopped, idle
	{"chronyd", "crashed", []string{"default"}},   // crashed
	{"cron", "", nil},                             // in no runlevel, not listed by rc-status: stopped
	{"devfs", "started", []string{"sysinit"}},     // base
	{"docker", "stopped", []string{"default"}},    // in the autostart, not running: idle
	{"edelweiss", "started", []string{"default"}}, // this interface
	{"marked", "failed", nil},                     // a script set mark_service_failed: rc-status says failed
	{"networking", "started", []string{"boot"}},   // network; nginx and sshd need it
	{"nginx", "started", []string{"default"}},     // a daemon with pid 4242 and a log file
	{"oldfail", "stopped", []string{"default"}},   // failed before the boot: stopped, idle
	{"proxy", "started", []string{"default"}},     // edelweiss needs it
	{"slow", "starting", []string{"default"}},     // has a failed marker, but is starting
	{"sshd", "started", []string{"default"}},      // has a failed marker and a boot failure, but runs
	{"t-fail", "stopped", []string{"default"}},    // its last start through the page failed (marker): failed
}

// syslog is /var/log/messages of the test host.
const syslog = `Oct 20 06:30:00 host daemon.err oldfail[100]: ERROR: oldfail failed to start
Oct 20 07:00:05 host syslog.info syslogd started: BusyBox v1.37.0
Oct 20 07:46:19 host daemon.err bootfail[302]: ERROR: bootfail failed to start
Oct 20 07:46:19 host daemon.err bootok[303]: ERROR: bootok failed to start
Oct 20 07:46:20 host daemon.err sshd[305]: ERROR: sshd failed to start
Oct 20 07:50:00 host daemon.info nginx[4242]: nginx started
Oct 20 07:51:00 host daemon.info cron[17]: not nginx
Oct 20 07:52:00 host daemon.notice nginx[4242]: reloading
Oct 20 07:55:00 host daemon.err bootlate[304]: ERROR: bootlate failed to start
`

// harness is the API on the test host below a temp root.
type harness struct {
	t        *testing.T
	root     string
	app      *fiber.App
	api      *API
	host     *run.Fake // what the API runs: rc-status and the relations of the services
	cmds     *run.Fake // what the jobs run
	launcher *launcher
	gate     chan struct{} // when set, the commands of the jobs wait until it is closed
	logged   func() string // what the API logged
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	f := &harness{t: t, root: t.TempDir()}
	f.logged = captureLogs(t)
	for _, s := range services {
		f.script(s.name)
		for _, rl := range s.runlevels {
			f.link("/etc/runlevels/"+rl+"/"+s.name, "/etc/init.d/"+s.name)
		}
	}
	f.script("net.lo")
	f.link("/etc/init.d/net.eth0", "net.lo")
	f.write("/run/openrc/softlevel", "default")
	for _, tool := range []string{rcService, rcUpdate, rcStatus, lbu} {
		f.file(tool, "", 0o755)
	}
	f.write("/proc/mounts", "/dev/sda2 / ext4 rw,relatime 0 0\nproc /proc proc rw 0 0\n")
	f.write("/proc/uptime", "3600.00 7000.00\n")
	f.write("/proc/1/stat", "") // init and the daemon of nginx run: Read looks for /proc/<pid>
	f.write("/proc/4242/stat", "")
	f.write("/run/openrc/daemons/nginx/001", "exec=/usr/sbin/nginx\npidfile=/run/nginx.pid\n")
	f.write("/run/nginx.pid", "4242\n")
	f.mark("failed", "t-fail", at("07:40:00"))
	f.mark("failed", "slow", at("07:41:00"))
	f.mark("failed", "sshd", at("07:42:00"))
	f.mark("ok", "bootok", at("07:50:00"))
	f.mark("ok", "bootlate", at("07:50:00"))
	f.write("/var/log/messages", syslog)
	var nginxLog strings.Builder
	for i := 1; i <= 600; i++ {
		fmt.Fprintf(&nginxLog, "GET /page/%d 200\n", i)
	}
	f.write("/var/log/nginx.log", nginxLog.String())

	// every other relation is none: the fake answers without output
	f.host = &run.Fake{Results: map[string]run.FakeResult{
		rcStatus + " --all --nocolor":     {Output: status()},
		rcService + " networking needsme": {Output: "nginx sshd \n"},
		rcService + " proxy needsme":      {Output: "edelweiss \n"},
		rcService + " nginx ineed":        {Output: "localmount net \n"},
		rcService + " nginx iuse":         {Output: "logger \n"},
		rcService + " nginx iafter":       {Output: "firewall \n"},
	}}
	f.cmds = &run.Fake{Results: map[string]run.FakeResult{}, OnRun: func(run.Cmd) (string, error) {
		if f.gate != nil {
			<-f.gate
		}
		return "", nil
	}}
	store := jobs.Store{Base: f.path("/run/edelweiss-ext-openrc/jobs"), LockPath: f.path("/run/edelweiss-ext-openrc/lock")}
	f.api = &API{Root: f.root, Runner: f.host, Tools: func() openrc.Tools { return openrc.FindTools(f.root) },
		Jobs: store, MarkDir: jobs.DefaultMarkDir, Now: func() time.Time { return now }}
	f.launcher = &launcher{t: t, cfg: jobs.RunnerConfig{Store: store, Now: time.Now,
		Exec: jobs.Executor{Root: f.root, Runner: f.cmds, MarkDir: jobs.DefaultMarkDir}}}
	f.api.Launcher = f.launcher
	t.Cleanup(f.launcher.wg.Wait) // a job that still runs must not write into a removed temp dir
	f.app = fiber.New(fiber.Config{ErrorHandler: sdk.ErrorHandler})
	Register(f.app, f.api)
	return f
}

// status is the output of rc-status --all --nocolor on the test host.
func status() string {
	var b strings.Builder
	b.WriteString("Runlevel: default\n")
	for _, s := range services {
		if s.state != "" {
			fmt.Fprintf(&b, " %-65s [  %s  ]\n", s.name, s.state)
		}
	}
	b.WriteString("Dynamic Runlevel: manual\n")
	return b.String()
}

// path is p, a path of the test host, below the root.
func (f *harness) path(p string) string { return filepath.Join(f.root, p) }

func (f *harness) file(p, content string, mode os.FileMode) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(f.path(p)), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(f.path(p), []byte(content), mode); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Chmod(f.path(p), mode); err != nil { // the umask does not apply to Chmod
		f.t.Fatal(err)
	}
}

func (f *harness) write(p, content string) { f.t.Helper(); f.file(p, content, 0o644) }

func (f *harness) remove(p string) {
	f.t.Helper()
	if err := os.RemoveAll(f.path(p)); err != nil {
		f.t.Fatal(err)
	}
}

func (f *harness) link(p, target string) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(f.path(p)), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Symlink(target, f.path(p)); err != nil {
		f.t.Fatal(err)
	}
}

// script writes the init script of the service, with its description.
func (f *harness) script(name string) {
	f.file("/etc/init.d/"+name, "#!/sbin/openrc-run\ndescription=\"The "+name+" service\"\n", 0o755)
}

// mark writes the marker of the runner (ok or failed) for the service, with the time.
func (f *harness) mark(kind, name string, when time.Time) {
	f.write(jobs.DefaultMarkDir+"/"+kind+"/"+name, when.UTC().Format(time.RFC3339))
}

// ran counts the commands of the API that start with prefix (a command line).
func (f *harness) ran(prefix string) int {
	n := 0
	for _, c := range f.host.Commands() {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// launcher is the Launcher of the tests. It hands the lock on as the real one does (a new descriptor of the same open
// file description, so that closing the API's own does not release it) and runs the runner in this process.
type launcher struct {
	t    *testing.T
	cfg  jobs.RunnerConfig
	err  error // when set, Launch fails with it
	mu   sync.Mutex
	dirs []string
	wg   sync.WaitGroup
}

func (l *launcher) Launch(dir string, lock *os.File) error {
	if l.err != nil {
		return l.err
	}
	fd, err := unix.Dup(int(lock.Fd()))
	if err != nil {
		return err
	}
	l.mu.Lock()
	l.dirs = append(l.dirs, dir)
	l.mu.Unlock()
	handed := os.NewFile(uintptr(fd), "lock")
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		defer handed.Close()
		if code := jobs.RunJob(l.cfg, dir, handed); code != 0 {
			l.t.Errorf("RunJob = %d; want 0", code)
		}
	}()
	return nil
}

func (l *launcher) launched() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.dirs)
}

// answer is a response read to its end.
type answer struct {
	status int
	raw    string
}

func (f *harness) send(method, path string, body any, u sdk.User) answer {
	f.t.Helper()
	var r io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		r = strings.NewReader(b)
	default:
		data, err := json.Marshal(b)
		if err != nil {
			f.t.Fatal(err)
		}
		r = bytes.NewReader(data)
	}
	return f.do(sdktest.Request(method, path, r, u))
}

func (f *harness) do(req *http.Request) answer {
	f.t.Helper()
	resp := sdktest.Do(f.t, f.app, req)
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		f.t.Fatal(err)
	}
	return answer{status: resp.StatusCode, raw: string(data)}
}

func (a answer) want(t *testing.T, status int) answer {
	t.Helper()
	if a.status != status {
		t.Fatalf("status %d (%s), want %d", a.status, a.raw, status)
	}
	return a
}

// wantError checks status and code of an error answer and returns its error object.
func (a answer) wantError(t *testing.T, status int, code string) map[string]any {
	t.Helper()
	a.want(t, status)
	var env struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal([]byte(a.raw), &env); err != nil {
		t.Fatalf("answer %q: %v", a.raw, err)
	}
	if env.Error["code"] != code {
		t.Fatalf("error %v, want the code %s", env.Error, code)
	}
	if msg, _ := env.Error["message"].(string); msg == "" {
		t.Errorf("error %v has no message", env.Error)
	}
	return env.Error
}

func decode[T any](t *testing.T, a answer) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(a.raw), &v); err != nil {
		t.Fatalf("answer %q: %v", a.raw, err)
	}
	return v
}

// captureLogs keeps what the API logs (and the SDK logs of the answers that are 500) out of the output of the tests,
// and returns what was logged so far.
func captureLogs(t *testing.T) func() string {
	t.Helper()
	w := &logWriter{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(w, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return w.String
}

type logWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *logWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *logWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// The name of a service in a path is decoded once and has to be a valid name; one that leaves the services is no
// route. Nothing is read for an invalid name; a valid one that is not among the services is not found.
func TestBadName(t *testing.T) {
	f := newHarness(t)
	routes := []string{"", "/logs", "/logs/stream?offset=0"}
	for _, name := range []string{"..", "%2e%2e", "%2E%2E", "..%2fetc", "%2e%2e%2fetc", "%252e%252e", "net%252eeth0",
		"a%20b", "sshd%0a", ".hidden", "-x", strings.Repeat("a", 65)} {
		for _, route := range routes {
			e := f.send("GET", "/services/"+name+route, nil, plain).wantError(t, http.StatusBadRequest, "invalid_input")
			if e["field"] != "service" {
				t.Errorf("%s%s: error %v; want the field service", name, route, e)
			}
		}
	}
	// an escape that is none: the URL parser of Go refuses to build it, so the request line is set as it is
	for _, route := range routes {
		req := sdktest.Request("GET", "/services/x", nil, plain)
		req.URL.Opaque = "/services/%zz" + route
		f.do(req).wantError(t, http.StatusBadRequest, "invalid_input")
	}
	if n := len(f.host.Commands()); n != 0 {
		t.Errorf("an invalid name ran %v", f.host.Commands())
	}
	for _, p := range []string{"/services/../x", "/services/../x/logs", "/services/x/../../jobs/x"} {
		f.send("GET", p, nil, plain).wantError(t, http.StatusNotFound, "not_found")
	}
	for _, route := range routes {
		f.send("GET", "/services/nosuch"+route, nil, plain).wantError(t, http.StatusNotFound, "not_found")
	}
	// a dot, a plus, a hyphen are part of a name; an escape is decoded
	for _, name := range []string{"net.eth0", "net%2eeth0", "t-fail"} {
		got := decode[Details](t, f.send("GET", "/services/"+name, nil, plain).want(t, http.StatusOK))
		if got.Name != strings.ReplaceAll(name, "%2e", ".") {
			t.Errorf("/services/%s answers %s", name, got.Name)
		}
	}
}
