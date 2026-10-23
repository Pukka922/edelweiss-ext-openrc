package jobs_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/jobs"
)

var idRe = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}-[0-9a-f]{4}$`)

// jobStore is a Store below a temp dir; neither its directories nor the lock file exist yet.
func jobStore(t *testing.T) jobs.Store {
	t.Helper()
	root := t.TempDir()
	return jobs.Store{Base: filepath.Join(root, "run/edelweiss-ext-openrc/jobs"), LockPath: filepath.Join(root, "run/edelweiss-ext-openrc/lock")}
}

// umask sets the umask for the test: the modes of the store must not depend on it.
func umask(t *testing.T, mask int) {
	old := syscall.Umask(mask)
	t.Cleanup(func() { syscall.Umask(old) })
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

// restartSpec is the job of a restart of sshd with a commit after the autostart changed.
var restartSpec = jobs.Spec{Kind: "restart", Target: "sshd", Steps: []jobs.Step{
	restartStep("sshd"),
	{Op: "exec", Label: "enable", Cmd: rcUpdate("add", "sshd", "default")},
	{Op: "exec", Label: "lbu", Cmd: lbuCommit()},
}}

func TestDefaultStore(t *testing.T) {
	if got := jobs.DefaultStore(); got != (jobs.Store{Base: "/run/edelweiss-ext-openrc/jobs", LockPath: "/run/edelweiss-ext-openrc/lock"}) {
		t.Errorf("DefaultStore() = %+v", got)
	}
}

func TestCreatePrunes(t *testing.T) {
	umask(t, 0o077)
	s := jobStore(t)
	lock := lockFor(t, s)
	writeFile(t, s.LockPath, "something longer than a job id, of an older version\n")
	start := time.Date(2026, 10, 20, 14, 0, 0, 0, time.FixedZone("CEST", 2*3600))
	var ids []string
	for i := range 12 {
		now := start.Add(time.Duration(i) * time.Minute)
		dir, st, err := s.Create(lock, restartSpec, now)
		if err != nil {
			t.Fatal(err)
		}
		if !idRe.MatchString(st.ID) || !strings.HasPrefix(st.ID, now.UTC().Format("20060102T150405")+"-") || dir != filepath.Join(s.Base, st.ID) {
			t.Fatalf("id %q, dir %q", st.ID, dir)
		}
		want := jobs.State{ID: st.ID, Kind: "restart", Target: "sshd", Status: jobs.Running, Steps: []string{"restart", "enable", "lbu"}, Started: now}
		if !reflect.DeepEqual(st, want) {
			t.Fatalf("state = %+v; want %+v", st, want)
		}
		ids = append(ids, st.ID)
	}
	// the newest 10 are kept
	entries, err := os.ReadDir(s.Base)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, e := range entries {
		kept = append(kept, e.Name())
	}
	if !reflect.DeepEqual(kept, ids[2:]) {
		t.Errorf("kept %v; want the newest 10 %v", kept, ids[2:])
	}

	// the files of the newest, their modes whatever the umask
	dir := filepath.Join(s.Base, ids[11])
	for path, want := range map[string]os.FileMode{s.Base: 0o755, filepath.Dir(s.Base): 0o755, dir: 0o755,
		filepath.Join(dir, "spec.json"): 0o600, filepath.Join(dir, "state.json"): 0o644} {
		if got := mode(t, path); got != want {
			t.Errorf("mode of %s = %v; want %v", path, got, want)
		}
	}
	var sp jobs.Spec
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(dir, "spec.json"))), &sp); err != nil {
		t.Fatal(err)
	}
	want := restartSpec
	want.ID = ids[11]
	if !reflect.DeepEqual(sp, want) {
		t.Errorf("spec.json = %+v; want %+v", sp, want)
	}
	var st jobs.State
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(dir, "state.json"))), &st); err != nil {
		t.Fatal(err)
	}
	if st.ID != ids[11] || st.Status != jobs.Running || !st.Started.Equal(start.Add(11*time.Minute)) || len(st.Steps) != 3 {
		t.Errorf("state.json = %+v", st)
	}
	// the lock file names the job created last, with a newline
	if got := readFile(t, s.LockPath); got != ids[11]+"\n" {
		t.Errorf("lock file = %q; want %q", got, ids[11]+"\n")
	}
}

// A job directory appears together with its files (it is made under a dot name and renamed into place): Newest never
// sees a job without its state, and a directory that a killed API left half-made is ignored.
func TestCreateAtomic(t *testing.T) {
	s := jobStore(t)
	lock := lockFor(t, s)
	writeFile(t, filepath.Join(s.Base, ".new-1234", "spec.json"), "{") // left behind by a killed API
	stop := make(chan struct{})
	failed := make(chan error, 1)
	go func() {
		defer close(failed)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := s.Newest(); err != nil {
				failed <- err
				return
			}
		}
	}()
	start := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for i := range 50 {
		if _, _, err := s.Create(lock, restartSpec, start.Add(time.Duration(i)*time.Second)); err != nil {
			close(stop)
			t.Fatal(err)
		}
	}
	close(stop)
	if err := <-failed; err != nil {
		t.Errorf("Newest while jobs were created: %v", err)
	}
	entries, err := os.ReadDir(s.Base)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	// the newest 10 and the half-made one, which is no job and stays; Create leaves no dot directory of its own
	if len(names) != 11 || names[0] != ".new-1234" || strings.HasPrefix(names[1], ".") {
		t.Errorf("job dir holds %v", names)
	}
	if newest, err := s.Newest(); err != nil || !strings.HasPrefix(newest.ID, "20261020T120049-") {
		t.Errorf("Newest = %+v, %v", newest, err)
	}
}

func TestGetInterrupted(t *testing.T) {
	s := jobStore(t)
	lock, err := s.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	_, created, err := s.Create(lock, restartSpec, now)
	if err != nil {
		t.Fatal(err)
	}
	status := func(id string) string {
		t.Helper()
		st, err := s.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if (st.Status == jobs.Interrupted) != (st.Code == "interrupted") {
			t.Errorf("status %q with code %q", st.Status, st.Code)
		}
		return st.Status
	}
	// running while the lock is held and names it
	if got := status(created.ID); got != jobs.Running {
		t.Errorf("with the lock held: %s; want running", got)
	}
	// a job created later, with the clock stepped back an hour (its id sorts older): the lock names it, so it is the
	// running one, and the first job lost its runner
	_, stepped, err := s.Create(lock, restartSpec, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if stepped.ID >= created.ID {
		t.Fatalf("ids %s, %s: want the second one older", created.ID, stepped.ID)
	}
	if a, b := status(created.ID), status(stepped.ID); a != jobs.Interrupted || b != jobs.Running {
		t.Errorf("after the second job: first %s, second %s; want interrupted, running", a, b)
	}
	if newest, err := s.Newest(); err != nil || newest.ID != stepped.ID || newest.Status != jobs.Running {
		t.Errorf("Newest = %+v, %v; want the running job", newest, err)
	}
	// the held lock names another job: neither runs
	writeFile(t, s.LockPath, "20261020T000000-0000\n")
	if a, b := status(created.ID), status(stepped.ID); a != jobs.Interrupted || b != jobs.Interrupted {
		t.Errorf("the lock names another job: %s, %s; want interrupted", a, b)
	}
	// the lock file names the job, but no one holds the lock: its runner is gone
	writeFile(t, s.LockPath, stepped.ID+"\n")
	lock.Close()
	if got := status(stepped.ID); got != jobs.Interrupted {
		t.Errorf("with the lock free: %s; want interrupted", got)
	}
	// a job that ended stays as it ended
	done := created
	done.Status = jobs.Done
	data, _ := json.Marshal(done)
	writeFile(t, filepath.Join(s.Base, created.ID, "state.json"), string(data))
	if st, err := s.Get(created.ID); err != nil || st.Status != jobs.Done {
		t.Errorf("a done job: %+v, %v", st, err)
	}

	// only ids of the form of Create; paths that lead to a job are none
	for _, id := range []string{"", "20261020T120000-ABCD", "20261020T120000-abcd", "../jobs/" + created.ID, created.ID + "/", "./" + created.ID} {
		if _, err := s.Get(id); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Get(%q) = %v; want a not-exist error", id, err)
		}
	}
}

func TestNewest(t *testing.T) {
	s := jobStore(t)
	if st, err := s.Newest(); st != nil || err != nil {
		t.Fatalf("Newest of no jobs = %+v, %v; want nil", st, err)
	}
	// create releases the lock after the job, so that Newest goes by the ids
	create := func(now time.Time) jobs.State {
		t.Helper()
		lock, err := s.TryLock()
		if err != nil {
			t.Fatal(err)
		}
		defer lock.Close()
		_, st, err := s.Create(lock, restartSpec, now)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	start := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for i := range 3 {
		create(start.Add(time.Duration(i) * time.Hour))
	}
	newest, err := s.Newest()
	if err != nil || newest == nil || !strings.HasPrefix(newest.ID, "20261020T140000-") {
		t.Fatalf("Newest = %+v, %v", newest, err)
	}
	if newest.Status != jobs.Interrupted { // as Get reads it: running without the lock
		t.Errorf("Newest status %q; want interrupted", newest.Status)
	}

	// jobs within one second: the later one is newer, whatever the random part of its id
	second := start.Add(3 * time.Hour)
	var same []string
	for i := range 6 {
		st := create(second.Add(time.Duration(i) * 100 * time.Millisecond))
		same = append(same, st.ID)
		if newest, err := s.Newest(); err != nil || newest.ID != st.ID {
			t.Fatalf("after %d jobs in one second Newest = %+v, %v; want %s", i+1, newest, err, st.ID)
		}
	}
	// and pruning keeps them: 9 jobs, all kept
	entries, _ := os.ReadDir(s.Base)
	if len(entries) != 9 || slices.ContainsFunc(same, func(id string) bool {
		_, err := os.Stat(filepath.Join(s.Base, id))
		return err != nil
	}) {
		t.Errorf("%d jobs kept", len(entries))
	}
}

func TestFail(t *testing.T) {
	s := jobStore(t)
	lock := lockFor(t, s)
	// Round(0) drops the monotonic reading, which a state read from its file does not have either
	dir, created, err := s.Create(lock, restartSpec, time.Now().Round(0))
	if err != nil {
		t.Fatal(err)
	}
	end := created.Started.Add(time.Second)
	if err := s.Fail(dir, "failed", "the runner could not start", end); err != nil {
		t.Fatal(err)
	}
	want := created
	want.Status, want.Code, want.Error, want.Finished = jobs.Failed, "failed", "the runner could not start", &end
	// the lock is still held and names the job: the end is what Get reads, not an interruption
	got, err := s.Get(created.ID)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Get = %+v, %v; want %+v", got, err, want)
	}
	if m := mode(t, filepath.Join(dir, "state.json")); m != 0o644 {
		t.Errorf("state.json mode %v; want 0644", m)
	}
	lock.Close()
	if got, err := s.Get(created.ID); err != nil || got.Status != jobs.Failed {
		t.Errorf("with the lock free: %+v, %v; want the job still failed", got, err)
	}
	// the end of a job is final
	if err := s.Fail(dir, "failed", "again", end.Add(time.Second)); err == nil {
		t.Error("Fail of a failed job: no error")
	}
	if got, err := s.Get(created.ID); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("after the second Fail: %+v, %v; want %+v", got, err, want)
	}
	if err := s.Fail(filepath.Join(s.Base, "20261020T000000-0000"), "failed", "x", end); err == nil {
		t.Error("Fail of a job that is not there: no error")
	}
}
