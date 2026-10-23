package jobs_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/jobs"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/run"
)

func TestReadMarks(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "run/edelweiss-ext-openrc")
	at := func(s string) time.Time {
		t.Helper()
		got, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	// nothing there: empty maps (not nil), no error
	failed, ok, err := jobs.ReadMarks(root, "")
	if err != nil || failed == nil || ok == nil || len(failed) != 0 || len(ok) != 0 {
		t.Fatalf("ReadMarks without directories = %v, %v, %v", failed, ok, err)
	}

	writeFile(t, filepath.Join(dir, "failed/sshd"), "2026-10-20T08:15:00Z")
	writeFile(t, filepath.Join(dir, "failed/net.eth0"), "  2026-10-20T10:15:00+02:00\n") // blanks and an offset
	writeFile(t, filepath.Join(dir, "failed/garbage"), "yesterday")
	writeFile(t, filepath.Join(dir, "failed/empty"), "")
	writeFile(t, filepath.Join(dir, "failed/date"), "2026-10-20")
	writeFile(t, filepath.Join(dir, "failed/.docker.tmp-123"), "2026-10-20T08:15:00Z") // a marker that is being written
	writeFile(t, filepath.Join(dir, "failed/with space"), "2026-10-20T08:15:00Z")      // no name of a service
	if err := os.Mkdir(filepath.Join(dir, "failed/adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "outside"), "2026-10-20T08:15:00Z")
	if err := os.Symlink(filepath.Join(root, "outside"), filepath.Join(dir, "failed/link")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "ok/sshd"), "2026-10-19T20:00:00Z")
	writeFile(t, filepath.Join(dir, "ok/docker"), "2026-10-20T07:00:00Z")

	failed, ok, err = jobs.ReadMarks(root, "")
	if err != nil {
		t.Fatal(err)
	}
	wantFailed := map[string]time.Time{"sshd": at("2026-10-20T08:15:00Z"), "net.eth0": at("2026-10-20T08:15:00Z")}
	wantOK := map[string]time.Time{"sshd": at("2026-10-19T20:00:00Z"), "docker": at("2026-10-20T07:00:00Z")}
	for _, c := range []struct {
		what      string
		got, want map[string]time.Time
	}{{"failed", failed, wantFailed}, {"ok", ok, wantOK}} {
		if len(c.got) != len(c.want) {
			t.Errorf("%s = %v, want %v", c.what, c.got, c.want)
		}
		for name, want := range c.want {
			if got, found := c.got[name]; !found || !got.Equal(want) {
				t.Errorf("%s[%s] = %v (%v), want %v", c.what, name, got, found, want)
			}
		}
	}

	// the directory of the markers, below the root, as the Executor takes it
	writeFile(t, filepath.Join(root, "var/marks/ok/ntpd"), "2026-10-20T09:00:00Z")
	if _, ok, err := jobs.ReadMarks(root, "/var/marks"); err != nil || len(ok) != 1 || !ok["ntpd"].Equal(at("2026-10-20T09:00:00Z")) {
		t.Errorf("ReadMarks(root, /var/marks) = %v, %v", ok, err)
	}

	// what the Executor writes is what ReadMarks reads
	x := jobs.Executor{Root: root, Runner: &run.Fake{Results: map[string]run.FakeResult{control("crond", "start").String(): {Status: 1}}}}
	before := time.Now().Truncate(time.Second)
	if err := x.Do(context.Background(), startStep("crond")); err == nil {
		t.Fatal("the start did not fail")
	}
	if err := x.Do(context.Background(), startStep("sshd")); err != nil {
		t.Fatal(err)
	}
	failed, ok, err = jobs.ReadMarks(root, jobs.DefaultMarkDir)
	if err != nil || failed["crond"].Before(before) || ok["sshd"].Before(before) || len(failed) != 2 || len(ok) != 2 {
		t.Errorf("after two steps: failed %v, ok %v, %v", failed, ok, err)
	}
	if _, found := failed["sshd"]; found {
		t.Error("the started service is still failed")
	}
}

// A marker directory or marker that cannot be read is an error, a missing directory is not.
func TestReadMarksErrors(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "run/edelweiss-ext-openrc")
	writeFile(t, filepath.Join(dir, "failed"), "not a directory")
	if failed, ok, err := jobs.ReadMarks(root, ""); err == nil || failed != nil || ok != nil {
		t.Errorf("ReadMarks with a file for a directory = %v, %v, %v, want an error and no maps", failed, ok, err)
	}
	if err := os.Remove(filepath.Join(dir, "failed")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "ok"), "not a directory")
	if _, _, err := jobs.ReadMarks(root, ""); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Errorf("ReadMarks with a file for ok/ = %v, want an error", err)
	}

	// a marker that cannot be opened (root opens it all)
	if os.Geteuid() == 0 {
		return
	}
	if err := os.Remove(filepath.Join(dir, "ok")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "ok/sshd"), "2026-10-20T08:15:00Z")
	if err := os.Chmod(filepath.Join(dir, "ok/sshd"), 0); err != nil {
		t.Fatal(err)
	}
	if failed, ok, err := jobs.ReadMarks(root, ""); !errors.Is(err, os.ErrPermission) || failed != nil || ok != nil {
		t.Errorf("ReadMarks with a marker that cannot be read = %v, %v, %v, want a permission error and no maps", failed, ok, err)
	}
}
