package openrc

import (
	"os"
	"path/filepath"
	"time"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/run"
)

// Env is the whole environment of every OpenRC command: run.Env, which has EINFO_COLOR=NO.
var Env = run.Env

// The time limits of the commands: start, stop and restart wait for a slow service (a docker stop takes its time),
// every other command gets 30 s.
const (
	controlTimeout = 5 * time.Minute
	queryTimeout   = 30 * time.Second
)

// Tools are the OpenRC programs and lbu, by their path on the host; "" when a tool is not there.
type Tools struct{ RcService, RcUpdate, RcStatus, Lbu string }

// binDirs are the directories FindTools looks in, in this order.
var binDirs = []string{"/usr/sbin", "/usr/bin", "/sbin", "/bin"}

// FindTools looks up each tool in /usr/sbin, /usr/bin, /sbin and /bin below root: the first that is an executable file
// there (a link to one counts). The paths it returns are those on the host, without root.
func FindTools(root string) Tools {
	find := func(name string) string {
		for _, dir := range binDirs {
			p := dir + "/" + name
			if st, err := os.Stat(filepath.Join(root, p)); err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0 {
				return p
			}
		}
		return ""
	}
	return Tools{RcService: find("rc-service"), RcUpdate: find("rc-update"), RcStatus: find("rc-status"), Lbu: find("lbu")}
}

// service is rc-service <s> <command>.
func (t Tools) service(s, command string, timeout time.Duration) run.Cmd {
	return run.Cmd{Path: t.RcService, Args: []string{s, command}, Timeout: timeout}
}

// Start starts the service (and what it needs).
func (t Tools) Start(s string) run.Cmd { return t.service(s, "start", controlTimeout) }

// Stop stops the service (and the services that need it).
func (t Tools) Stop(s string) run.Cmd { return t.service(s, "stop", controlTimeout) }

// Restart stops and starts the service; OpenRC starts the services that need it again in the background.
func (t Tools) Restart(s string) run.Cmd { return t.service(s, "restart", controlTimeout) }

// Enable adds the service to the runlevel (its autostart).
func (t Tools) Enable(s, runlevel string) run.Cmd {
	return run.Cmd{Path: t.RcUpdate, Args: []string{"add", s, runlevel}, Timeout: queryTimeout}
}

// Disable takes the service out of the runlevel.
func (t Tools) Disable(s, runlevel string) run.Cmd {
	return run.Cmd{Path: t.RcUpdate, Args: []string{"del", s, runlevel}, Timeout: queryTimeout}
}

// LbuCommit saves the changes of /etc to the boot medium (diskless and data mode).
func (t Tools) LbuCommit() run.Cmd {
	return run.Cmd{Path: t.Lbu, Args: []string{"commit"}, Timeout: queryTimeout}
}

// status lists every runlevel with its services and their states, without colour.
func (t Tools) status() run.Cmd {
	return run.Cmd{Path: t.RcStatus, Args: []string{"--all", "--nocolor"}, Timeout: queryTimeout}
}

// query asks rc-service about the relations of the service: ineed, iuse, iafter or needsme.
func (t Tools) query(s, what string) run.Cmd { return t.service(s, what, queryTimeout) }
