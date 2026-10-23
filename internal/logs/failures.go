package logs

import (
	"regexp"
	"time"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/openrc"
)

// failedStart is the message OpenRC logs under the service's name when its start fails, at a boot (a runlevel change)
// as from rc-service: "daemon.err t-fail[302]: ERROR: t-fail failed to start" (the capture).
var failedStart = regexp.MustCompile(`^ERROR: (\S+) failed to start$`)

// syslogStarted is the line busybox syslogd writes when it starts, so at every boot, in its own form (the capture):
// "Oct 20 07:46:19 a73377ff9ea9 syslog.info syslogd started: BusyBox v1.37.0".
var syslogStarted = regexp.MustCompile(`^[A-Z][a-z]{2} [ 0-3][0-9] [0-9]{2}:[0-9]{2}:[0-9]{2} \S+ ` +
	`syslog\.info syslogd started: BusyBox v`)

// BootFailures are the services whose start failed since the boot, with the time of the last failure of each: the
// lines of failedStart in the last 4 MiB of the system log below root (/var/log/messages.0, then /var/log/messages, as
// Tail reads them) that name the service they are logged under. syslogd writes no year: a line is of the year of now,
// or of the year before when that would be after now. Without a /var/log/messages that can be read there are none, and
// no error; the error is one of reading it.
//
// Which lines are of this boot is told in the clock of the log: those after the last start line of busybox syslogd
// (syslogStarted), whatever their time. A host without a real-time clock (a Raspberry Pi) writes the lines of its boot
// with the clock it has until NTP sets it (the time of the last shutdown, or 1970), so that their times are before now
// less the uptime. Only when the 4 MiB hold no start line (another syslog daemon, or a log that has grown past it since
// the boot) are they the lines with a time after bootTime; byBoot tells that the boot time decided.
func BootFailures(root string, bootTime, now time.Time) (failed map[string]time.Time, byBoot bool, err error) {
	failed = map[string]time.Time{}
	f, _, err := open(root, syslogPath)
	if err != nil {
		return failed, false, nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return failed, false, err
	}
	lines, _, err := sourceLines(root, Source{ID: syslogID, Path: syslogPath}, f, st.Size())
	if err != nil {
		return failed, false, err
	}
	from := lastStart(lines)
	byBoot = from < 0
	for _, line := range lines[max(from, 0):] {
		e, ok := parse(line)
		if m := failedStart.FindStringSubmatch(e.message); !ok || m == nil || m[1] != e.program || !openrc.ValidName(m[1]) {
			continue
		}
		at, ok := logTime(e.time, now)
		if ok && (!byBoot || at.After(bootTime)) && at.After(failed[e.program]) {
			failed[e.program] = at
		}
	}
	return failed, byBoot, nil
}

// lastStart is the index of the line after the last start line of syslogd, -1 when there is none.
func lastStart(lines []string) int {
	for i := len(lines) - 1; i >= 0; i-- {
		if syslogStarted.MatchString(lines[i]) {
			return i + 1
		}
	}
	return -1
}

// logTime is the time of a line of the system log, written without a year ("Oct 20 07:46:19"), in the location of
// now: in the year of now, or in the year before when that would be after now.
func logTime(written string, now time.Time) (time.Time, bool) {
	t, err := time.Parse("Jan _2 15:04:05", written)
	if err != nil {
		return time.Time{}, false
	}
	in := func(year int) time.Time {
		return time.Date(year, t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, now.Location())
	}
	if at := in(now.Year()); !at.After(now) {
		return at, true
	}
	return in(now.Year() - 1), true
}
