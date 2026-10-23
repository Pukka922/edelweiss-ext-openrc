package jobs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

// maxOutput is how much of a failed command's output the state keeps (its end).
const maxOutput = 4096

// serviceCgroup is the cgroup of the OpenRC service edelweiss, whose cleanup kills everything that runs in it when
// edelweiss stops or restarts.
const serviceCgroup = "openrc.edelweiss"

// The cgroups of the runner: the file that lists them, and the cgroup it moves to when it is in serviceCgroup. Variables
// so that a test can point them elsewhere.
var (
	procCgroup = "/proc/self/cgroup"
	escapeDir  = "/sys/fs/cgroup/edelweiss-openrc"
)

// RunnerConfig configures RunJob. RequireRoot makes the runner refuse a job directory that is not root's.
type RunnerConfig struct {
	Store       Store
	Exec        Executor
	RequireRoot bool
	Now         func() time.Time
}

// RunJob is the runner (the binary started with --run-job <dir>): it runs the steps of the job in dir, holding the job
// lock it got from the API as lock. It returns the exit status of the runner: 0 when the job ran, whatever its result,
// 2 when it could not start (not a job directory, lock not the held job lock file or naming another job, a job that
// has run before, an unreadable spec); the reason goes to stderr, and also into the log of the job when the runner
// made it before.
//
// Before each step and at the end it writes state.json: Done, Failed with the code, the message and the output of the
// failed step, or Interrupted when a SIGTERM came (the running step ends, no further one starts). SIGHUP and SIGINT
// are ignored, by the commands too. Each step and its outcome go into the log (0600) as Describe gives them, and so
// does a marker that could not be written (Executor.Note) and a failure to leave the cgroup of the service edelweiss
// (the job runs on).
func RunJob(cfg RunnerConfig, dir string, lock *os.File) int {
	syscall.Umask(0o022)
	signal.Ignore(syscall.SIGHUP, syscall.SIGINT)
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM)
	defer signal.Stop(term)
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	dir, err := cfg.checkDir(dir)
	if err != nil {
		return refuse(err)
	}
	// fd 3 must be the lock file itself (flock takes any file, a shell's descriptor too), hold the lock, and name this
	// job: Create named it there for the API, which handed the lock on
	if err := cfg.Store.holds(lock); err != nil {
		return refuse(fmt.Errorf("the job lock is not ours: %w", err))
	}
	id, err := named(lock)
	if err != nil {
		return refuse(fmt.Errorf("reading the job lock: %w", err))
	}
	if id != filepath.Base(dir) {
		return refuse(fmt.Errorf("the job lock names job %q, not %s", id, filepath.Base(dir)))
	}
	unix.CloseOnExec(int(lock.Fd())) // no command may keep the lock after the runner (a service leaves a daemon behind)
	// the log is new: a job runs once. From here on the job has its log, the one place that the runner, whose stderr is
	// the null device, can say what happened.
	f, err := os.OpenFile(filepath.Join(dir, logFile), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return refuse(err) // the log is that of a job that ran before, or cannot be made: not ours to write to
	}
	f.Close()
	r := &runner{cfg: cfg, dir: dir}
	r.cfg.Exec.Note = r.note
	if err := escapeCgroup(procCgroup, escapeDir, os.Getpid()); err != nil {
		r.note("the runner could not leave the cgroup %s, a stop of that service may take it along: %v", serviceCgroup, err)
	}
	if err := readJSON(filepath.Join(dir, specFile), &r.spec); err != nil {
		return r.refuse(fmt.Errorf("reading %s: %w", specFile, err))
	}
	if err := readJSON(filepath.Join(dir, stateFile), &r.st); err != nil {
		return r.refuse(fmt.Errorf("reading %s: %w", stateFile, err))
	}
	r.run(term)
	return 0
}

// refuse is RunJob's end when the job could not start.
func refuse(err error) int {
	fmt.Fprintln(os.Stderr, "run-job:", err)
	return 2
}

// refuse is RunJob's end when the job could not start after its log was made: the reason goes into the log too.
func (r *runner) refuse(err error) int {
	r.note("refused: %v", err)
	return refuse(err)
}

// runner is the job RunJob runs.
type runner struct {
	cfg  RunnerConfig
	dir  string
	spec Spec
	st   State
}

func (r *runner) run(term <-chan os.Signal) {
	st, steps := &r.st, r.spec.Steps
	st.Status, st.Step, st.Code, st.Error, st.Output, st.Finished = Running, 0, "", "", "", nil
	st.Steps = make([]string, len(steps))
	for i, s := range steps {
		st.Steps[i] = s.Label
	}
	r.note("job %s: %s %s, %d steps", st.ID, st.Kind, st.Target, len(steps))
	for i, s := range steps {
		st.Step = i
		if terminated(term) {
			st.Status, st.Code = Interrupted, codeInterrupted
			st.Error = fmt.Sprintf("stopped by SIGTERM before step %d of %d", i+1, len(steps))
			r.note("%s", st.Error)
			break
		}
		r.save()
		r.note("step %d/%d: %s", i+1, len(steps), Describe(s))
		err := r.cfg.Exec.Do(context.Background(), s)
		if err == nil {
			r.note("step %d/%d: ok", i+1, len(steps))
			continue
		}
		var se *StepError
		if !errors.As(err, &se) {
			se = &StepError{Code: codeFailed, Message: err.Error()}
		}
		st.Status, st.Code = Failed, se.Code
		st.Error = se.Message
		st.Output = tail(se.Output, maxOutput)
		r.note("step %d/%d: %s: %s", i+1, len(steps), st.Code, st.Error)
		if st.Output != "" {
			r.note("output:\n    %s", strings.ReplaceAll(st.Output, "\n", "\n    "))
		}
		break
	}
	if st.Status == Running {
		st.Status = Done
	}
	finished := r.cfg.Now()
	st.Finished = &finished
	r.note("finished: %s", st.Status)
	r.save()
}

// terminated reports whether a SIGTERM came.
func terminated(term <-chan os.Signal) bool {
	select {
	case <-term:
		return true
	default:
		return false
	}
}

// save writes the state; a failure is noted in the log, and the job goes on.
func (r *runner) save() {
	if err := writeJSON(filepath.Join(r.dir, stateFile), r.st, 0o644); err != nil {
		r.note("writing the state: %v", err)
	}
}

// note appends a line to the log. The log is opened for each line: the runner keeps no file open but the lock.
func (r *runner) note(format string, args ...any) {
	f, err := os.OpenFile(filepath.Join(r.dir, logFile), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", r.cfg.Now().UTC().Format(time.RFC3339), fmt.Sprintf(format, args...))
}

// tail is the end of s, at most n bytes, cut at the start of a rune.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	i := len(s) - n
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	return s[i:]
}

// checkDir returns the absolute path of dir when it is a job directory: a real directory (no symlink) directly below
// Store.Base with the name of a job id, owned by root when RequireRoot.
func (cfg RunnerConfig) checkDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	base, err := filepath.Abs(cfg.Store.Base)
	if err != nil {
		return "", err
	}
	if filepath.Dir(abs) != base || !idPattern.MatchString(filepath.Base(abs)) {
		return "", fmt.Errorf("%s is not a job directory", dir)
	}
	fi, err := os.Lstat(abs)
	if err != nil || !fi.IsDir() {
		return "", fmt.Errorf("%s is not a job directory", dir)
	}
	if cfg.RequireRoot {
		if st, ok := fi.Sys().(*syscall.Stat_t); !ok || st.Uid != 0 {
			return "", fmt.Errorf("%s is not owned by root", dir)
		}
	}
	return abs, nil
}

// escapeCgroup moves the process pid out of its cgroup (listed in the file cgroups) into the cgroup dir when that is
// the cgroup of the OpenRC service edelweiss: its cleanup would kill the runner with edelweissd, and a restart of
// edelweiss is a job like any other. Best effort: on failure the runner stays where it is, and the caller gets the
// error. A process that is in another cgroup stays there without an error and without a trace, and so does one without
// a cgroup file (a kernel without cgroups).
func escapeCgroup(cgroups, dir string, pid int) error {
	data, err := os.ReadFile(cgroups)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.Contains(string(data), serviceCgroup) {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "cgroup.procs"), []byte(fmt.Sprint(pid)), 0o644)
}
