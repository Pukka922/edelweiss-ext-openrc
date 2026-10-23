//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/run"
)

var ctx = context.Background()

// exe runs the commands as the extension runs them: no shell, no stdin, "/" as the working directory and the
// environment of the extension, run.Env (with EINFO_COLOR=NO: OpenRC without colour).
var exe = run.Exec{Env: run.Env}

// toolDirs are the directories the extension looks for the OpenRC tools in, in this order; the first hit counts.
var toolDirs = []string{"/usr/sbin", "/usr/bin", "/sbin", "/bin"}

// statusForms are the forms of "rc-status --all --nocolor" that are tried in this order; the first that works is
// the form the extension uses.
var statusForms = [][]string{{"--all", "--nocolor"}, {"--all", "-C"}, {"-a", "-C"}}

// findTool is the first executable file called name in toolDirs.
func findTool(t *testing.T, name string) string {
	t.Helper()
	for _, dir := range toolDirs {
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 {
			return p
		}
	}
	t.Fatalf("%s is in none of %v", name, toolDirs)
	return ""
}

// noFinalNewline lists the blocks whose text lacked the final newline (block added one).
var noFinalNewline []string

// block writes a block of a capture to the test log: a line "=== name ===" and the text below it. The output of the
// commands reaches the log unchanged; a text without final newline gets one, and its name goes to noFinalNewline.
func block(name, text string) {
	fmt.Printf("=== %s ===\n%s", name, text)
	if text != "" && !strings.HasSuffix(text, "\n") {
		fmt.Println()
		noFinalNewline = append(noFinalNewline, name)
	}
}

// withStatus is out below a line with the exit status, as start-fail.txt holds it.
func withStatus(out string, status int) string {
	return fmt.Sprintf("exit status: %d\n%s", status, out)
}

// fileBlock writes the file as a block "<prefix> <path>" with its content.
func fileBlock(t *testing.T, prefix, path string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block(prefix+" "+path, string(b))
}

// fileBlocks writes every regular file below dir as a block "<prefix> <path>" with its content.
func fileBlocks(t *testing.T, prefix, dir string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			fileBlock(t, prefix, p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// capture runs the commands of a test and keeps the exit status of each.
type capture struct {
	t                       *testing.T
	service, update, status string   // rc-service, rc-update, rc-status
	statusArgs              []string // the form of rc-status --all --nocolor that works
	statuses                []string
}

func newCapture(t *testing.T) *capture {
	return &capture{t: t, service: findTool(t, "rc-service"), update: findTool(t, "rc-update"), status: findTool(t, "rc-status")}
}

// run runs path with args and returns what the command printed (stdout and stderr together) and its exit status. A
// command that did not run, or was killed, fails the test.
func (c *capture) run(path string, args ...string) (string, int) {
	c.t.Helper()
	cmd := run.Cmd{Path: path, Args: args, Timeout: 5 * time.Minute}
	out, err := exe.Run(ctx, cmd)
	got := 0
	if err != nil {
		var e *run.Error
		if !errors.As(err, &e) || e.Status < 0 {
			c.t.Fatalf("the command did not run: %v", err)
		}
		got = e.Status
	}
	c.statuses = append(c.statuses, fmt.Sprintf("%3d  %s", got, cmd))
	return out, got
}

// step is run for a command that has to end with status 0.
func (c *capture) step(path string, args ...string) string {
	c.t.Helper()
	out, got := c.run(path, args...)
	if got != 0 {
		c.t.Errorf("%s %s: exit status %d\n%s", path, strings.Join(args, " "), got, out)
	}
	return out
}

// sh is a step of /bin/sh -c script: helpers (find, ls, tail, logger, ps) that are no commands of the extension.
func (c *capture) sh(script string) string {
	c.t.Helper()
	return c.step("/bin/sh", "-c", script)
}

// rcStatus is the output of rc-status --all --nocolor. The first call tries statusForms in order and keeps the first
// that works.
func (c *capture) rcStatus() string {
	c.t.Helper()
	if c.statusArgs != nil {
		return c.step(c.status, c.statusArgs...)
	}
	for _, args := range statusForms {
		if out, got := c.run(c.status, args...); got == 0 {
			c.statusArgs = args
			return out
		}
	}
	c.t.Fatalf("no form of rc-status --all --nocolor works: %v", statusForms)
	return ""
}

// finish ends the log of a test: the exit status of every command, the blocks that lacked the final newline, "end".
func (c *capture) finish() {
	block("statuses", strings.Join(c.statuses, "\n")+"\n")
	block("blocks-without-final-newline", strings.Join(noFinalNewline, "\n")+"\n")
	block("end", "")
}

// writeService installs an init script with the given description and body (the lines after the description).
func writeService(t *testing.T, name, description, body string) {
	t.Helper()
	script := "#!/sbin/openrc-run\ndescription=\"" + description + "\"\n" + body
	if err := os.WriteFile("/etc/init.d/"+name, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// readPID is the process id in a pid file.
func readPID(t *testing.T, file string) int {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	return pid
}

// waitFor polls cond every 100 ms for up to 10 s and fails the test when it does not become true.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timeout: %s", what)
}

func fileHas(path, text string) bool {
	b, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(b), text)
}

// kill ends the process with SIGKILL, as kill -9 does, and waits until it is gone.
func kill(t *testing.T, pid int) {
	t.Helper()
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "process "+strconv.Itoa(pid)+" is gone", func() bool { return !exists("/proc/" + strconv.Itoa(pid)) })
}
