// Package openrc reads the services of OpenRC below a root ("/" on the host, a temp dir in tests): the scripts of
// /etc/init.d, their autostart in /etc/runlevels, their states from rc-status and the state files of /run/openrc, and
// their relations from rc-service. It also builds the command lines that change them. The formats are those of the
// real OpenRC of Alpine (0.62 and 0.63), captured in testdata.
package openrc

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/run"
)

// runDir is the state directory of OpenRC, below the root.
const runDir = "/run/openrc"

// State is the state of a service as OpenRC reports it.
type State string

// The states of a service. OpenRC never sets Failed for a start that failed (the service is stopped then); it is
// there only for a marker that a script set with mark_service_failed.
const (
	Started  State = "started"
	Stopped  State = "stopped"
	Crashed  State = "crashed" // started, but its daemon is gone
	Failed   State = "failed"
	Starting State = "starting"
	Stopping State = "stopping"
	Inactive State = "inactive"
)

// states are the states Read takes from rc-status; it prints others too (scheduled), which Read leaves to /run/openrc.
var states = []State{Started, Stopped, Crashed, Failed, Starting, Stopping, Inactive}

// Service is a service of /etc/init.d with its state.
type Service struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	State       State    `json:"state"`
	OneShot     bool     `json:"oneShot"`    // started without a tracked daemon
	Supervised  bool     `json:"supervised"` // supervise-daemon
	Runlevels   []string `json:"runlevels"`  // the runlevels it is added to
	PID         int      `json:"pid,omitempty"`
	Uptime      string   `json:"uptime,omitempty"` // as rc-status prints it, when it does
	Script      string   `json:"script"`           // /etc/init.d/<s>
}

// Snapshot is what Read found.
type Snapshot struct {
	Present  bool      // rc-service found and /run/openrc/softlevel exists
	Services []Service // sorted by name
}

// Reader reads the services of the system below Root; Runner runs the Tools.
type Reader struct {
	Root   string
	Runner run.Runner
	Tools  Tools
}

// Read lists the services of /etc/init.d with their description, runlevels and state. The state (and the uptime of a
// supervised service) is what rc-status --all --nocolor prints; a service it does not list (it leaves out the stopped
// services that are in no runlevel) gets the state of its directory in /run/openrc, stopped when there is none. A
// started service whose daemon is gone is crashed (see addDaemons).
// Without OpenRC (no rc-service, or no /run/openrc/softlevel: OpenRC did not run) it is Snapshot{Present: false} and
// no error. A failing rc-status is an error, and so is an entry of /etc/runlevels or /run/openrc that cannot be read
// (only a missing one means "none").
func (r Reader) Read(ctx context.Context) (Snapshot, error) {
	if r.Tools.RcService == "" {
		return Snapshot{}, nil
	}
	if ok, err := lookup(filepath.Join(r.Root, runDir, "softlevel")); !ok || err != nil {
		return Snapshot{}, err
	}
	services, err := r.scan()
	if err != nil {
		return Snapshot{}, err
	}
	if r.Tools.RcStatus == "" {
		return Snapshot{}, errors.New("rc-status is not installed")
	}
	out, err := r.Runner.Run(ctx, r.Tools.status())
	if err != nil {
		return Snapshot{}, err
	}
	printed := parseRcStatus(out)
	levels, err := r.runlevels()
	if err != nil {
		return Snapshot{}, err
	}
	for i := range services {
		s := &services[i]
		s.Runlevels = levels[s.Name]
		if s.Runlevels == nil {
			s.Runlevels = []string{}
		}
		if l, ok := printed[s.Name]; ok {
			s.State, s.Uptime = l.state, l.uptime
		} else if s.State, err = r.marked(s.Name); err != nil {
			return Snapshot{}, err
		}
		if err := r.addDaemons(s); err != nil {
			return Snapshot{}, err
		}
	}
	return Snapshot{Present: true, Services: services}, nil
}

// listed is a service as rc-status prints it: its state and, for a supervised one, its uptime.
type listed struct {
	state  State
	uptime string
}

// statusLine is a service line of rc-status: a space, the name, at least one space, the state in brackets.
var statusLine = regexp.MustCompile(`^ (\S+)\s+\[\s*(.*?)\s*\]$`)

// respawns is the respawn count after the uptime of a supervised service: "started 00:00:02 (0)".
var respawns = regexp.MustCompile(`\s*\(\d+\)$`)

// parseRcStatus reads the output of rc-status --all --nocolor: the state of each service it lists, by name. The text
// between the state and the respawn count is the uptime ("00:00:02"). A service listed under several runlevels counts
// once. Headings, lines of a state that is not one of states, and every other line are skipped: in a container lines
// about the read-only cgroup come before and in between.
func parseRcStatus(out string) map[string]listed {
	found := map[string]listed{}
	for _, line := range strings.Split(out, "\n") {
		m := statusLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		word, rest, _ := strings.Cut(m[2], " ")
		state := State(word)
		if _, seen := found[m[1]]; seen || !slices.Contains(states, state) {
			continue
		}
		found[m[1]] = listed{state: state, uptime: strings.TrimSpace(respawns.ReplaceAllString(rest, ""))}
	}
	return found
}

// markerStates are the state directories of /run/openrc (a link per service) that Read looks at, in this order, for a
// service rc-status did not list. failed/ is not among them: only rc-status says failed.
var markerStates = []State{Stopping, Starting, Inactive, Started}

// marked is the state of the first directory of markerStates that holds the service, else stopped.
func (r Reader) marked(name string) (State, error) {
	for _, state := range markerStates {
		if ok, err := lookup(filepath.Join(r.Root, runDir, string(state), name)); ok || err != nil {
			return state, err
		}
	}
	return Stopped, nil
}

// addDaemons sets what the state files of the service's daemons tell: Supervised when supervise-daemon keeps the files
// of its child in options/<s>/; Crashed for a started service whose daemon is gone; OneShot when it is started without
// a daemon; the PID while it is started.
//
// A daemon is gone when the pid file of its entry in daemons/<s>/ names a process that /proc does not have (for a
// supervised service that is the supervisor, as for OpenRC). rc-status checks the same, but without root it cannot
// look at the processes of root and says started. A pid file that is missing, cannot be read or holds no pid, and a
// /proc that cannot tell (see process), leave the state as it is.
func (r Reader) addDaemons(s *Service) error {
	options := filepath.Join(r.Root, runDir, "options", s.Name)
	child, err := lookup(filepath.Join(options, "child_pid"))
	if err != nil {
		return err
	}
	since, err := lookup(filepath.Join(options, "start_time"))
	if err != nil {
		return err
	}
	s.Supervised = child || since
	daemons, err := r.daemons(s.Name)
	if err != nil || s.State != Started {
		return err
	}
	if len(daemons) == 0 && !s.Supervised {
		s.OneShot = true
		return nil
	}
	var pids []int // of the daemon entries, known ones only
	for _, d := range daemons {
		if p := d["pidfile"]; path.IsAbs(p) {
			if pid := readPID(filepath.Join(r.Root, p)); pid > 0 {
				if r.process(pid) == gone {
					s.State, s.Uptime = Crashed, ""
					return nil
				}
				pids = append(pids, pid)
			}
		}
	}
	switch {
	case s.Supervised: // the child, not the supervisor; none while the supervisor starts it again
		if pid := readPID(filepath.Join(options, "child_pid")); r.process(pid) != gone {
			s.PID = pid
		}
	case len(pids) > 0:
		s.PID = pids[0]
	}
	return nil
}

// liveness is whether a process runs, as far as /proc tells.
type liveness int

const (
	unknown liveness = iota
	running
	gone
)

// process tells whether the process pid runs: by its entry in /proc below the root. /proc can only tell when it shows
// init (/proc/1): without /proc, or with hidepid hiding the processes of other users, every process is unknown, and so
// is pid 0.
func (r Reader) process(pid int) liveness {
	if pid <= 0 {
		return unknown
	}
	if ok, err := lookup(filepath.Join(r.Root, "proc", "1")); !ok || err != nil {
		return unknown
	}
	switch ok, err := lookup(filepath.Join(r.Root, "proc", strconv.Itoa(pid))); {
	case err != nil:
		return unknown
	case ok:
		return running
	}
	return gone
}

// daemons are the daemons OpenRC tracks for the service, one per file of daemons/<name>/ (001, 002, …, in this order):
// the key=value lines of the file (exec, argv_<n>, pidfile). No directory is no daemon; a directory or file that
// cannot be read is an error.
func (r Reader) daemons(name string) ([]map[string]string, error) {
	dir := filepath.Join(r.Root, runDir, "daemons", name)
	entries, err := readDir(dir)
	if err != nil {
		return nil, err
	}
	var daemons []map[string]string
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if errors.Is(err, fs.ErrNotExist) {
			continue // the service stopped since the listing
		} else if err != nil {
			return nil, err
		}
		d := map[string]string{}
		for _, line := range strings.Split(string(b), "\n") {
			if k, v, ok := strings.Cut(line, "="); ok {
				d[k] = v
			}
		}
		daemons = append(daemons, d)
	}
	return daemons, nil
}

// Programs are the names under which the system log has the service's lines: its own name (OpenRC logs under it) and
// the base names of its daemons' programs (exec= of daemons/<name>/*), sorted, each once. supervise-daemon logs under
// its own name, which is not among them. An invalid name has none; daemons that cannot be read add none.
func (r Reader) Programs(name string) []string {
	if !ValidName(name) {
		return nil
	}
	names := []string{name}
	daemons, _ := r.daemons(name)
	for _, d := range daemons {
		if exec := d["exec"]; exec != "" {
			names = append(names, path.Base(exec))
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// readPID is the process id in a pid file (digits and a newline), 0 when there is none: the file is missing, cannot be
// read, is no regular file (a FIFO would block the read) or holds something else.
func readPID(file string) int {
	if st, err := os.Stat(file); err != nil || !st.Mode().IsRegular() {
		return 0
	}
	f, err := os.Open(file)
	if err != nil {
		return 0
	}
	defer f.Close()
	b, _ := io.ReadAll(io.LimitReader(f, 32))
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}

// lookup reports whether there is an entry at p (a link counts, also one whose target is not there). Only a missing
// entry is "no"; any other error (a directory that cannot be searched) is returned.
func lookup(p string) (bool, error) {
	_, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// readDir is the content of the directory, sorted by name: nothing when it is not there, an error when it cannot be
// read.
func readDir(dir string) ([]fs.DirEntry, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return entries, err
}
