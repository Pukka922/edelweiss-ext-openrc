package openrc

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/run"
)

// fixture is a fake system below a temp root: the files, links and directories of /etc/init.d, /etc/runlevels and
// /run/openrc that the Reader reads. Paths are those of the system ("/etc/init.d/sshd"); the fixture writes them below
// its root, making the directories as needed.
type fixture struct {
	t    *testing.T
	root string
}

func newFixture(t *testing.T) *fixture { return &fixture{t: t, root: t.TempDir()} }

// path is p below the root.
func (f *fixture) path(p string) string { return filepath.Join(f.root, p) }

func (f *fixture) dir(p string) {
	f.t.Helper()
	if err := os.MkdirAll(f.path(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) file(p, content string, mode os.FileMode) {
	f.t.Helper()
	f.dir(filepath.Dir(p))
	if err := os.WriteFile(f.path(p), []byte(content), mode); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Chmod(f.path(p), mode); err != nil { // the umask does not apply to Chmod
		f.t.Fatal(err)
	}
}

func (f *fixture) link(p, target string) {
	f.t.Helper()
	f.dir(filepath.Dir(p))
	if err := os.Symlink(target, f.path(p)); err != nil {
		f.t.Fatal(err)
	}
}

// lock makes p unreadable (mode 0000) until the test ends.
func (f *fixture) lock(p string) {
	f.t.Helper()
	if err := os.Chmod(f.path(p), 0); err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = os.Chmod(f.path(p), 0o755) }) // before the temp dir is removed
}

// script writes /etc/init.d/<name>, executable.
func (f *fixture) script(name, text string) { f.file(initDir+"/"+name, text, 0o755) }

// service writes a script with the description and adds it to the runlevels, with the link rc-update makes.
func (f *fixture) service(name, description string, runlevels ...string) {
	f.script(name, "#!/sbin/openrc-run\ndescription=\""+description+"\"\n")
	for _, rl := range runlevels {
		f.link(runlevelDir+"/"+rl+"/"+name, initDir+"/"+name)
	}
}

// mark puts the service into a state directory of /run/openrc (started, starting, failed, …), as OpenRC does.
func (f *fixture) mark(state, name string) { f.link(runDir+"/"+state+"/"+name, initDir+"/"+name) }

// tools installs the OpenRC tools where Alpine has them (the capture found them there).
func (f *fixture) tools() {
	for _, tool := range []string{"/sbin/rc-service", "/sbin/rc-update", "/bin/rc-status"} {
		f.file(tool, "", 0o755)
	}
}

// openrc installs the tools and the softlevel file: OpenRC is present.
func (f *fixture) openrc() {
	f.tools()
	f.file(runDir+"/softlevel", "default", 0o644)
}

// reader is a Reader of the fixture whose runner answers rc-status --all --nocolor with status (exit 0) and every
// other command without output.
func (f *fixture) reader(status string) (Reader, *run.Fake) {
	tools := FindTools(f.root)
	fake := &run.Fake{Results: map[string]run.FakeResult{tools.status().String(): {Output: status}}}
	return Reader{Root: f.root, Runner: fake, Tools: tools}, fake
}

// readTestdata is the content of testdata/<name>.
func readTestdata(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// blockLine is the line above each block of a capture file.
var blockLine = regexp.MustCompile(`^=== (.+) ===$`)

// blocks splits a capture file into its blocks: the text below each "=== name ===" line, by name.
func blocks(t *testing.T, name string) map[string]string {
	t.Helper()
	out := map[string]string{}
	current, text := "", ""
	for _, line := range strings.SplitAfter(readTestdata(t, name), "\n") {
		if m := blockLine.FindStringSubmatch(strings.TrimSuffix(line, "\n")); m != nil {
			if current != "" {
				out[current] = text
			}
			current, text = m[1], ""
			continue
		}
		text += line
	}
	if current != "" {
		out[current] = text
	}
	return out
}

// capturedServices are the test services of integration/testdata/init.d that the capture was made with; t-hang, whose
// start hangs, came later, for the test of the time limit.
var capturedServices = []string{"t-crash", "t-daemon", "t-fail", "t-log", "t-needs", "t-oneshot", "t-super"}

// fromCapture rebuilds the system of the capture (testdata/README.md) below a new root: the test services of
// integration/testdata/init.d it was made with, /etc/runlevels and /run/openrc entry by entry as "ls -lAR" listed them
// in run-openrc.txt (directories, the files with their captured content, the links, the FIFO of supervise-daemon), the
// pid files of pidfiles.txt, a /proc entry for init and each process the capture ran (pid files, supervise-daemon's
// child), /var/run as a link to /run and the OpenRC tools at the paths the capture found them. The runner answers
// rc-status with the captured output in the file status.
func fromCapture(t *testing.T, status string) (Reader, *run.Fake) {
	t.Helper()
	f := newFixture(t)
	for _, name := range capturedServices {
		b, err := os.ReadFile(filepath.Join("../../integration/testdata/init.d", name))
		if err != nil {
			t.Fatalf("test service: %v", err)
		}
		f.script(name, string(b))
	}
	captured := blocks(t, "run-openrc.txt")
	for _, listing := range []string{"run-openrc-ls", "etc-runlevels"} {
		dir := ""
		for _, line := range strings.Split(captured[listing], "\n") {
			fields := strings.Fields(line)
			switch {
			case strings.HasPrefix(line, "/") && strings.HasSuffix(line, ":"):
				dir = strings.TrimSuffix(line, ":")
				f.dir(dir)
				continue
			case len(fields) == 0 || fields[0] == "total":
				continue
			case len(fields) != 9 && (len(fields) != 11 || fields[9] != "->"):
				t.Fatalf("%s: not a line of ls -l: %q", listing, line)
			}
			p := dir + "/" + fields[8]
			switch fields[0][0] {
			case 'd':
				f.dir(p)
			case '-':
				content, ok := captured["file "+p]
				if !ok {
					t.Fatalf("run-openrc.txt has no content of %s", p)
				}
				if strings.HasPrefix(p, runDir+"/options/") {
					content = strings.TrimSuffix(content, "\n") // the capture added it (README)
				}
				f.file(p, content, 0o644)
			case 'l':
				f.link(p, fields[10])
			case 'p':
				if err := syscall.Mkfifo(f.path(p), 0o600); err != nil {
					t.Fatal(err)
				}
			default:
				t.Fatalf("%s: unexpected type in %q", listing, line)
			}
		}
	}
	// The processes of the capture: init, the daemons of the pid files and supervise-daemon's child.
	f.dir("/proc/1")
	f.dir("/proc/" + strings.TrimSpace(captured["file "+runDir+"/options/t-super/child_pid"]))
	for name, content := range blocks(t, "pidfiles.txt") {
		f.file(strings.TrimPrefix(name, "file "), content, 0o644)
		f.dir("/proc/" + strings.TrimSpace(content))
	}
	f.link("/var/run", "../run") // as on Alpine: t-super's daemon entry names /var/run/supervise-t-super.pid
	f.tools()
	return f.reader(readTestdata(t, status))
}
