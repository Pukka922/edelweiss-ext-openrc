package jobs_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/jobs"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/run"
)

// marker is the file of the marker of a service below the root of x: kind is "ok" or "failed", in the default
// directory of the markers.
func marker(x jobs.Executor, kind, service string) string {
	return filepath.Join(x.Root, "run/edelweiss-ext-openrc", kind, service)
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// seen is the time a marker file holds; it fails the test unless the file holds exactly an RFC 3339 time in UTC.
func seen(t *testing.T, path string) time.Time {
	t.Helper()
	text := readFile(t, path)
	got, err := time.Parse(time.RFC3339, text)
	if err != nil {
		t.Fatalf("%s holds %q: %v", path, text, err)
	}
	if got.UTC().Format(time.RFC3339) != text {
		t.Errorf("%s holds %q, want the time in UTC (%s)", path, text, got.UTC().Format(time.RFC3339))
	}
	return got
}

func TestDefaultMarkDir(t *testing.T) {
	if jobs.DefaultMarkDir != "/run/edelweiss-ext-openrc" {
		t.Errorf("DefaultMarkDir = %q", jobs.DefaultMarkDir)
	}
}

// What a step with a Mark leaves for the view: success writes the ok marker and removes the failed one, the failure
// of a start writes the failed marker, the failure of a stop writes nothing.
func TestMarkOutcomes(t *testing.T) {
	const (
		old  = "2026-10-01T10:00:00Z" // the content of a marker that was there before; wanted as it is: untouched
		now  = "now"                  // wanted: a new time
		none = ""                     // before: no marker; wanted: no marker
	)
	ignored := startStep("sshd")
	ignored.Ignore = true
	for _, c := range []struct {
		name         string
		step         jobs.Step
		status       int // of the command; -1: did not start
		okBefore     string
		failedBefore string
		wantOK       string
		wantFailed   string
		fails        bool // Do returns an error
	}{
		{name: "start works", step: startStep("sshd"), okBefore: old, failedBefore: old, wantOK: now, wantFailed: none},
		{name: "start works, nothing before", step: startStep("sshd"), wantOK: now},
		{name: "restart works", step: restartStep("sshd"), okBefore: old, failedBefore: old, wantOK: now, wantFailed: none},
		{name: "stop works", step: stopStep("sshd"), okBefore: old, failedBefore: old, wantOK: now, wantFailed: none},
		{name: "start fails", step: startStep("sshd"), status: 1, okBefore: old, wantOK: old, wantFailed: now, fails: true},
		{name: "start fails, a failed marker before", step: startStep("sshd"), status: 1, failedBefore: old, wantFailed: now, fails: true},
		{name: "restart fails", step: restartStep("sshd"), status: 1, okBefore: old, wantOK: old, wantFailed: now, fails: true},
		{name: "start does not start", step: startStep("sshd"), status: -1, wantFailed: now, fails: true},
		{name: "stop fails", step: stopStep("sshd"), status: 1, okBefore: old, failedBefore: old, wantOK: old, wantFailed: old, fails: true},
		{name: "stop fails, nothing before", step: stopStep("sshd"), status: 1, fails: true},
		{name: "start fails but is ignored", step: ignored, status: 1, okBefore: old, wantOK: old, wantFailed: now},
	} {
		t.Run(c.name, func(t *testing.T) {
			x, fake := executor(t)
			x.Note = func(format string, args ...any) { t.Errorf("a marker that works: note "+format, args...) } // nothing to say
			okFile, failedFile := marker(x, "ok", "sshd"), marker(x, "failed", "sshd")
			other := marker(x, "failed", "docker")
			writeFile(t, other, old)
			if c.okBefore != none {
				writeFile(t, okFile, c.okBefore)
			}
			if c.failedBefore != none {
				writeFile(t, failedFile, c.failedBefore)
			}
			if c.status != 0 {
				fake.Results = map[string]run.FakeResult{c.step.Cmd.String(): {Output: "boom\n", Status: c.status}}
			}
			start := time.Now().Truncate(time.Second)
			err := x.Do(context.Background(), c.step)
			end := time.Now()
			if (err != nil) != c.fails {
				t.Fatalf("Do = %v, want an error: %v", err, c.fails)
			}
			if err != nil && stepError(t, err).Code != "failed" {
				t.Errorf("the error of the step: %v", err)
			}
			for _, f := range []struct{ path, want string }{{okFile, c.wantOK}, {failedFile, c.wantFailed}} {
				switch f.want {
				case none:
					if exists(f.path) {
						t.Errorf("%s exists: %q", f.path, readFile(t, f.path))
					}
				case now:
					if got := seen(t, f.path); got.Before(start) || got.After(end) {
						t.Errorf("%s holds %v, want a time between %v and %v", f.path, got, start, end)
					}
				default:
					if got := readFile(t, f.path); got != f.want {
						t.Errorf("%s holds %q, want it untouched (%q)", f.path, got, f.want)
					}
				}
			}
			if got := readFile(t, other); got != old {
				t.Errorf("the marker of another service: %q", got)
			}
		})
	}
}

// A step without a Mark leaves nothing behind, not even the directories.
func TestNoMarkNoFiles(t *testing.T) {
	x, _ := executor(t)
	steps := []jobs.Step{
		{Op: "exec", Label: "enable", Cmd: rcUpdate("add", "sshd", "default"), Service: "sshd"}, // a service without a Mark
		{Op: "exec", Label: "lbu", Cmd: lbuCommit()},
	}
	for _, s := range steps {
		if err := x.Do(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}
	if entries, _ := os.ReadDir(x.Root); len(entries) != 0 {
		t.Errorf("the steps left %v", entries)
	}
}

// The markers are read by the view of every user: the modes do not depend on the umask, and a marker is written whole
// (no temporary file stays).
func TestMarkFiles(t *testing.T) {
	umask(t, 0o077)
	x, _ := executor(t)
	for _, s := range []jobs.Step{startStep("sshd"), stopStep("net.eth0")} {
		if err := x.Do(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}
	base := filepath.Join(x.Root, "run/edelweiss-ext-openrc")
	for path, want := range map[string]os.FileMode{
		filepath.Dir(base): 0o755, base: 0o755, filepath.Join(base, "ok"): 0o755,
		marker(x, "ok", "sshd"): 0o644, marker(x, "ok", "net.eth0"): 0o644,
	} {
		if got := mode(t, path); got != want {
			t.Errorf("mode of %s = %v, want %v", path, got, want)
		}
	}
	var names []string
	entries, err := os.ReadDir(filepath.Join(base, "ok"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if want := []string{"net.eth0", "sshd"}; !reflect.DeepEqual(names, want) {
		t.Errorf("ok/ holds %v, want %v", names, want)
	}
	// the failed marker too
	fake := &run.Fake{Results: map[string]run.FakeResult{control("sshd", "start").String(): {Status: 1}}}
	x.Runner = fake
	if err := x.Do(context.Background(), startStep("sshd")); err == nil {
		t.Fatal("the start did not fail")
	}
	for path, want := range map[string]os.FileMode{filepath.Join(base, "failed"): 0o755, marker(x, "failed", "sshd"): 0o644} {
		if got := mode(t, path); got != want {
			t.Errorf("mode of %s = %v, want %v", path, got, want)
		}
	}
}

// The time of a marker is in UTC, whatever the time zone of the machine.
func TestMarkTimeIsUTC(t *testing.T) {
	old := time.Local
	time.Local = time.FixedZone("CEST", 2*3600)
	t.Cleanup(func() { time.Local = old })
	x, _ := executor(t)
	start := time.Now().Truncate(time.Second)
	if err := x.Do(context.Background(), startStep("sshd")); err != nil {
		t.Fatal(err)
	}
	if got := seen(t, marker(x, "ok", "sshd")); got.Before(start) || got.After(time.Now()) {
		t.Errorf("the marker holds %v, want a time since %v", got, start)
	}
}

// MarkDir is below Root like every file of the Executor.
func TestMarkDir(t *testing.T) {
	x, _ := executor(t)
	x.MarkDir = "/var/lib/marks"
	if err := x.Do(context.Background(), startStep("sshd")); err != nil {
		t.Fatal(err)
	}
	seen(t, filepath.Join(x.Root, "var/lib/marks/ok/sshd"))
	if exists(marker(x, "ok", "sshd")) {
		t.Error("the marker is in the default directory too")
	}
}

// The service of a Mark is a file name: only the name of a service goes there. A step that names anything else does
// not run its command.
func TestMarkRefusesWhatIsNoMark(t *testing.T) {
	for _, c := range []struct{ service, mark string }{
		{"", "start"}, {"..", "start"}, {".", "stop"}, {"../x", "start"}, {"a/b", "stop"}, {".hidden", "start"},
		{"with space", "start"}, {"/etc/passwd", "stop"}, {"sshd\n", "start"}, {strings.Repeat("a", 65), "start"},
		{"sshd", "pause"}, {"sshd", "Start"}, {"sshd", "ok"},
	} {
		x, fake := executor(t)
		s := jobs.Step{Op: "exec", Label: "start", Cmd: control("sshd", "start"), Service: c.service, Mark: c.mark}
		se := stepError(t, x.Do(context.Background(), s))
		if se.Code != "failed" || se.Message == "" || len(fake.Calls) != 0 {
			t.Errorf("service %q, mark %q: %+v, %d commands ran", c.service, c.mark, se, len(fake.Calls))
		}
		if entries, _ := os.ReadDir(x.Root); len(entries) != 0 {
			t.Errorf("service %q, mark %q: left %v", c.service, c.mark, entries)
		}
	}
	// without a Mark the service is not looked at
	x, fake := executor(t)
	enable := jobs.Step{Op: "exec", Label: "enable", Cmd: rcUpdate("add", "sshd", "boot"), Service: "../x"}
	if err := x.Do(context.Background(), enable); err != nil || len(fake.Calls) != 1 {
		t.Errorf("a step without a Mark: %v, %d commands", err, len(fake.Calls))
	}
}

// The marker is only a note for the view: when it cannot be written, the step is what the command made of it, and the
// reason goes to Note.
func TestMarkWriteFailure(t *testing.T) {
	var notes []string
	note := func(format string, args ...any) { notes = append(notes, fmt.Sprintf(format, args...)) }
	for _, c := range []struct {
		name, blocked string // the directory that is a file
		status        int
	}{
		{"ok marker of a start that works", "ok", 0},
		{"failed marker of a start that works", "failed", 0}, // the old failed marker cannot go
		{"failed marker of a start that fails", "failed", 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			notes = nil
			x, fake := executor(t)
			x.Note = note
			if c.status != 0 {
				fake.Results = map[string]run.FakeResult{control("sshd", "start").String(): {Output: "boom\n", Status: c.status}}
			}
			blocked := filepath.Join(x.Root, "run/edelweiss-ext-openrc", c.blocked)
			writeFile(t, blocked, "")
			err := x.Do(context.Background(), startStep("sshd"))
			if c.status == 0 && err != nil {
				t.Errorf("a start that works: %v", err)
			}
			if c.status != 0 {
				if se := stepError(t, err); se.Message != "exec /sbin/rc-service sshd start: exit 1" || se.Output != "boom" {
					t.Errorf("a start that fails: %+v", se)
				}
			}
			if len(notes) == 0 || !strings.Contains(notes[0], "sshd") || !strings.Contains(notes[0], blocked) {
				t.Errorf("notes %q, want one that names the service and %s", notes, blocked)
			}
			// Note is optional
			x.Note = nil
			_ = x.Do(context.Background(), startStep("sshd"))
		})
	}
}

// A reader of a marker never sees a half-written one.
func TestMarkAtomic(t *testing.T) {
	x, _ := executor(t)
	path := marker(x, "ok", "sshd")
	if err := x.Do(context.Background(), startStep("sshd")); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	done := make(chan string, 1)
	go func() {
		reads := 0
		for {
			select {
			case <-stop:
				done <- ""
				return
			default:
			}
			data, err := os.ReadFile(path)
			if err != nil {
				done <- err.Error()
				return
			}
			if _, err := time.Parse(time.RFC3339, string(data)); err != nil {
				done <- fmt.Sprintf("read %q after %d reads: %v", data, reads, err)
				return
			}
			reads++
		}
	}()
	for range 300 {
		if err := x.Do(context.Background(), startStep("sshd")); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	if msg := <-done; msg != "" {
		t.Error(msg)
	}
}
