package run

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// TestHelper is the program the Exec tests run: the test binary itself, started as
// "<binary> -test.run=^TestHelper$ -- <mode>". In a normal test run there is no mode and it does nothing. The mode is
// an argument and not an environment variable, so that TestExecEnv can check an environment of exactly Env.
func TestHelper(t *testing.T) {
	if flag.NArg() != 1 {
		return
	}
	switch flag.Arg(0) {
	case "stdin": // whether stdin is the null device, then what a read of it gives; exit 3
		in, err := os.Stdin.Stat()
		null, nullErr := os.Stat(os.DevNull)
		fmt.Println("null device:", err == nil && nullErr == nil && os.SameFile(in, null))
		buf := make([]byte, 16)
		n, err := os.Stdin.Read(buf)
		fmt.Printf("read: %d bytes, %v\n", n, err)
		os.Exit(3)
	case "exit1":
		os.Exit(1)
	case "sleep":
		time.Sleep(time.Minute)
		os.Exit(0)
	case "env": // the working directory, then the environment, one line each; exit 4 (envExit)
		dir, _ := os.Getwd()
		fmt.Println("dir=" + dir)
		for _, e := range os.Environ() {
			fmt.Println(e)
		}
		os.Exit(envExit)
	case "fork": // a child that sleeps with this stdout and stderr, as a daemon that is left behind; exit 0
		child := exec.Command(os.Args[0], "-test.run=^TestHelper$", "--", "sleep")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		fmt.Printf("child %d\n", child.Process.Pid)
		os.Exit(0)
	}
}

// envExit is the status of the env mode, which its Cmd takes as success: with the race detector a program that exits 0
// waits a second for late race reports first.
const envExit = 4

// helper is the Cmd that runs TestHelper in a mode. The path is absolute: Exec runs it in "/".
func helper(t *testing.T, mode string) Cmd {
	t.Helper()
	bin, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	return Cmd{Path: bin, Args: []string{"-test.run=^TestHelper$", "--", mode}}
}

// A program has no stdin: it reads the null device, whatever stdin the extension itself has.
func TestExecNoStdin(t *testing.T) {
	c := helper(t, "stdin")
	c.OK = []int{3}
	out, err := Exec{}.Run(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if want := "null device: true\nread: 0 bytes, EOF\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestExecOKStatus(t *testing.T) {
	c := helper(t, "exit1")
	_, err := Exec{}.Run(context.Background(), c)
	var e *Error
	if !errors.As(err, &e) || e.Status != 1 {
		t.Fatalf("Run = %v, want an *Error with Status 1", err)
	}
	if e.Path != c.Path || !reflect.DeepEqual(e.Args, c.Args) {
		t.Errorf("Error names %s %q, want %s %q", e.Path, e.Args, c.Path, c.Args)
	}
	c.OK = []int{1}
	if _, err := (Exec{}).Run(context.Background(), c); err != nil {
		t.Fatalf("Run with OK [1] = %v, want success", err)
	}
}

func TestExecTimeout(t *testing.T) {
	c := helper(t, "sleep")
	c.Timeout = 200 * time.Millisecond
	start := time.Now()
	_, err := Exec{}.Run(context.Background(), c)
	var e *Error
	if !errors.As(err, &e) || e.Status != -1 {
		t.Fatalf("Run = %v, want an *Error with Status -1", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Run = %v, want it to wrap context.DeadlineExceeded", err)
	}
	if d := time.Since(start); d > 20*time.Second {
		t.Errorf("Run took %v: the timeout did not stop it", d)
	}
}

// The scripts of TestExecTimeoutEndsTheGroup, in the directory of its markers ($1). The leader starts the child in the
// background and waits; SIGTERM makes it wait for the marker of the child, leave its own and exit 7. The child leaves
// a marker on SIGTERM and goes on, so that only SIGKILL ends it.
const (
	leaderScript = `#!/bin/sh
dir=$1
trap 'until [ -e "$dir/child" ]; do sleep 0.05; done; : >"$dir/leader"; exit 7' TERM
"$dir/child.sh" "$dir" &
echo "child $!"
until [ -e "$dir/ready" ]; do sleep 0.05; done
wait
`
	childScript = `#!/bin/sh
trap ': >"$1/child"' TERM
: >"$1/ready"
while :; do sleep 1; done
`
)

// gone reports whether the process has ended: it is not there, or it is a zombie that its new parent has not reaped yet.
func gone(pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true
	}
	_, after, ok := strings.Cut(string(b), ") ")
	return ok && strings.HasPrefix(after, "Z")
}

// A timeout sends SIGTERM to the whole process group of the program, so that openrc-run can roll a half-done start or
// stop back on its way out, and then SIGKILL to what is left of the group. It is a failure whatever the program exited
// with after SIGTERM, and Run does not wait for the grace period when the program ends before it.
func TestExecTimeoutEndsTheGroup(t *testing.T) {
	dir := t.TempDir()
	for name, script := range map[string]string{"leader.sh": leaderScript, "child.sh": childScript} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	c := Cmd{Path: filepath.Join(dir, "leader.sh"), Args: []string{dir}, Timeout: 2 * time.Second}
	start := time.Now()
	out, err := Exec{}.Run(context.Background(), c)
	took := time.Since(start)
	var pid int
	if _, scanErr := fmt.Sscanf(out, "child %d", &pid); scanErr != nil || pid <= 0 {
		t.Fatalf("output %q: want the pid of the child", out)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	var e *Error
	if !errors.As(err, &e) || e.Status != -1 || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Run = %v, want an *Error with Status -1 that wraps context.DeadlineExceeded (not the exit 7 after SIGTERM)", err)
	}
	for _, marker := range []string{"leader", "child"} {
		if _, err := os.Stat(filepath.Join(dir, marker)); err != nil {
			t.Errorf("no marker of the %s: SIGTERM did not reach it (%v)", marker, err)
		}
	}
	if took > 10*time.Second {
		t.Errorf("Run took %v: it waited out the grace period although the program ended on SIGTERM", took)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !gone(pid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !gone(pid) {
		t.Errorf("the child %d, which ignores SIGTERM, is still there: the rest of the group was not killed", pid)
	}
}

// A program that ends well but leaves a child behind that keeps its output open is a success, at once: Run neither
// waits for the child nor fails.
func TestExecLingeringChild(t *testing.T) {
	// No second of waiting at exit 0 under the race detector (see envExit), so that "at once" can be measured.
	x := Exec{Env: append(slices.Clone(Env), "GORACE=atexit_sleep_ms=0")}
	start := time.Now()
	out, err := x.Run(context.Background(), helper(t, "fork"))
	var pid int
	if _, scanErr := fmt.Sscanf(out, "child %d", &pid); scanErr == nil {
		t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	}
	if err != nil {
		t.Fatalf("Run = %q, %v: want success", out, err)
	}
	if pid == 0 {
		t.Errorf("output %q: want the child's pid", out)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("Run took %v: it waited for the child", d)
	}
}

func TestExecNotStarted(t *testing.T) {
	_, err := Exec{}.Run(context.Background(), Cmd{Path: "/nonexistent/edelweiss-tool", Args: []string{"x"}})
	var e *Error
	if !errors.As(err, &e) || e.Status != -1 {
		t.Fatalf("Run = %v, want an *Error with Status -1", err)
	}
}

func TestExecEnv(t *testing.T) {
	if want := []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C", "EINFO_COLOR=NO"}; !reflect.DeepEqual(Env, want) {
		t.Errorf("Env = %q, want %q", Env, want)
	}
	c := helper(t, "env")
	c.OK = []int{envExit}
	out, err := Exec{}.Run(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if got[0] != "dir=/" {
		t.Errorf("working directory: %q, want dir=/", got[0])
	}
	if !reflect.DeepEqual(got[1:], Env) {
		t.Errorf("environment %q, want exactly %q", got[1:], Env)
	}
	own := []string{"PATH=/bin", "X=1"}
	out, err = Exec{Env: own}.Run(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(strings.TrimSuffix(out, "\n"), "\n"); !reflect.DeepEqual(got[1:], own) {
		t.Errorf("environment %q, want exactly %q", got[1:], own)
	}
}

func TestTimeoutOfCmd(t *testing.T) {
	if DefaultTimeout != 30*time.Second {
		t.Errorf("DefaultTimeout = %v, want 30s", DefaultTimeout)
	}
	for _, tc := range []struct{ set, want time.Duration }{
		{0, 30 * time.Second}, {-1, 0}, {-time.Hour, 0}, {5 * time.Second, 5 * time.Second},
	} {
		if got := (Cmd{Timeout: tc.set}).timeout(); got != tc.want {
			t.Errorf("Timeout %v: limit %v, want %v", tc.set, got, tc.want)
		}
	}
}

func TestCmdString(t *testing.T) {
	c := Cmd{Path: "/sbin/rc-update", Args: []string{"add", "sshd", "default"}, Timeout: time.Minute}
	if got := c.String(); got != "/sbin/rc-update add sshd default" {
		t.Errorf("String() = %q", got)
	}
	if got := (Cmd{Path: "/usr/sbin/lbu"}).String(); got != "/usr/sbin/lbu" {
		t.Errorf("String() without args = %q", got)
	}
}

func TestErrorMessage(t *testing.T) {
	inner := errors.New("exit status 2")
	e := &Error{Path: "/sbin/rc-service", Args: []string{"sshd", "start"}, Status: 2, Output: "sshd: failed", Err: inner}
	if got := e.Error(); got != "/sbin/rc-service sshd start: exit status 2: sshd: failed" {
		t.Errorf("Error() = %q", got)
	}
	if !errors.Is(e, inner) {
		t.Error("Unwrap does not give the error of the process")
	}
	e.Output = ""
	if got := e.Error(); got != "/sbin/rc-service sshd start: exit status 2" {
		t.Errorf("Error() without output = %q", got)
	}
}

func TestFake(t *testing.T) {
	stop := Cmd{Path: "/sbin/rc-service", Args: []string{"docker", "stop"}, Timeout: 5 * time.Minute}
	status := Cmd{Path: "/bin/rc-status", Args: []string{"--all", "--nocolor"}, OK: []int{1}}
	var seen []string
	f := &Fake{
		Results: map[string]FakeResult{
			stop.String():   {Output: "docker: not stopped\n", Status: 2},
			status.String(): {Output: "crashed services\n", Status: 1},
		},
		OnRun: func(c Cmd) (string, error) {
			seen = append(seen, c.String())
			return "from OnRun", nil
		},
	}
	ctx := context.Background()
	out, err := f.Run(ctx, stop)
	var e *Error
	if !errors.As(err, &e) || e.Status != 2 || out != "docker: not stopped\n" || e.Output != "docker: not stopped" {
		t.Errorf("stop: %q, %v: want the output and an *Error with Status 2", out, err)
	}
	if out, err := f.Run(ctx, status); err != nil || out != "crashed services\n" {
		t.Errorf("rc-status exit 1 with OK [1]: %q, %v, want success", out, err)
	}
	other := Cmd{Path: "/sbin/rc-service", Args: []string{"sshd", "start"}}
	if out, err := f.Run(ctx, other); err != nil || out != "from OnRun" {
		t.Errorf("OnRun: %q, %v", out, err)
	}
	if !reflect.DeepEqual(seen, []string{"/sbin/rc-service sshd start"}) {
		t.Errorf("OnRun saw %q: only the call without a result", seen)
	}
	want := []string{stop.String(), status.String(), other.String()}
	if got := f.Commands(); !reflect.DeepEqual(got, want) {
		t.Errorf("Commands = %q, want %q", got, want)
	}
	if f.Calls[0].Cmd.Timeout != 5*time.Minute || f.Calls[2].Cmd.String() != other.String() {
		t.Errorf("calls: %+v", f.Calls)
	}
	// a command that did not start
	gone := Cmd{Path: "/sbin/gone"}
	f.Results[gone.String()] = FakeResult{Status: -1}
	if _, err := f.Run(ctx, gone); !errors.As(err, &e) || e.Status != -1 || err.Error() != "/sbin/gone: did not start" {
		t.Errorf("did not start: %v", err)
	}
	if out, err := (&Fake{}).Run(ctx, other); out != "" || err != nil {
		t.Errorf("an empty Fake: %q, %v, want success without output", out, err)
	}
}

func TestFakeConcurrent(t *testing.T) {
	f := &Fake{}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = f.Run(context.Background(), Cmd{Path: "/bin/true"})
		}()
	}
	wg.Wait()
	if n := len(f.Commands()); n != 20 {
		t.Errorf("%d calls recorded, want 20", n)
	}
}
