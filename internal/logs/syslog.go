package logs

import (
	"path"
	"regexp"
	"slices"
	"strings"
)

// syslogLine is a line of /var/log/messages as busybox syslogd writes it (the capture): the time ("Oct 20 07:46:19", a
// day below 10 padded with a space), the host, facility.level ("daemon.err"; "<13>" for a priority without a name),
// then the text.
var syslogLine = regexp.MustCompile(`^([A-Z][a-z]{2} [ 0-3][0-9] [0-9]{2}:[0-9]{2}:[0-9]{2}) \S+ (?:[a-z0-9]+\.[a-z]+|<[0-9]+>) (.*)$`)

// entry is a line of the system log: its time as written, the program that logged it (the tag) and its message.
type entry struct {
	time, program, message string
}

// parse splits a line of the system log. The text after the priority is "<tag>[<pid>]: <message>" or
// "<tag>: <message>"; syslogd's own first line ("syslogd started: BusyBox v1.37.0") has no ":" right after its tag, so
// the tag ends at the first space, "[" or ":" (a text that starts with one has no program). ok is false for any other
// line.
func parse(line string) (e entry, ok bool) {
	m := syslogLine.FindStringSubmatch(line)
	if m == nil {
		return entry{}, false
	}
	text := m[2]
	end := strings.IndexAny(text, " [:")
	if end < 0 {
		end = len(text)
	}
	rest := text[end:]
	if strings.HasPrefix(rest, "[") {
		if i := strings.IndexByte(rest, ']'); i > 0 {
			rest = rest[i+1:]
		}
	}
	return entry{time: m[1], program: text[:end], message: strings.TrimPrefix(strings.TrimPrefix(rest, ":"), " ")}, true
}

// SyslogProgram is the program of a busybox syslogd line, "" if none (format from the capture).
func SyslogProgram(line string) string {
	e, _ := parse(line)
	return e.program
}

// generic are programs that run all kinds of services: their name in the system log does not tell whose line it is.
var generic = []string{"sh", "ash", "bash", "dash", "busybox", "env", "nice", "ionice", "su", "sudo", "doas",
	"start-stop-daemon"}

// supervisor is supervise-daemon, which logs under its own name for each service it supervises.
const supervisor = "supervise-daemon"

// programSet are the programs whose lines of the system log are a service's.
type programSet map[string]bool

// matcher is the set of programs (the service's name and the base names of its daemons' programs, as
// openrc.Reader.Programs has them) without the generic ones.
func matcher(programs []string) programSet {
	set := programSet{}
	for _, p := range programs {
		if p != "" && !slices.Contains(generic, p) {
			set[p] = true
		}
	}
	return set
}

// match reports whether the line of the system log is one of the programs': logged under one of their names, or by
// supervise-daemon about one of them (supervised).
func (set programSet) match(line string) bool {
	e, ok := parse(line)
	switch {
	case !ok:
		return false
	case e.program == supervisor:
		return set[supervised(e.message)]
	}
	return set[e.program]
}

// supervised is the program a message of supervise-daemon is about, in the forms of the capture (the supervisor of
// t-super, whose daemon is /bin/sleep; the last two were seen while the capture was explored):
//
//	Supervisor command line: supervise-daemon t-super --start … /bin/sleep -- 100000   the service: t-super
//	Child command line: /bin/sleep 100000                                              its program: sleep
//	/bin/sleep, pid 198, terminated by signal 9                                        sleep
//	stopping /bin/sleep, pid 514                                                       sleep
//
// It is "" for the other messages, which name none ("Will stop PID 476", "Sending signal 15 to PID 476"). The program
// is named by its command only (/bin/sleep, or sleep when the service gives no path): a line about /bin/sleep is one
// of every service that runs sleep.
func supervised(message string) string {
	if rest, ok := strings.CutPrefix(message, "Supervisor command line: "+supervisor+" "); ok {
		service, _, _ := strings.Cut(rest, " ")
		return service
	}
	if rest, ok := strings.CutPrefix(message, "Child command line: "); ok {
		exec, _, _ := strings.Cut(rest, " ")
		return path.Base(exec)
	}
	if exec, _, ok := strings.Cut(strings.TrimPrefix(message, "stopping "), ", pid "); ok {
		return path.Base(exec)
	}
	return ""
}
