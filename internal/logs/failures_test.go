package logs

import (
	"os"
	"testing"
	"time"
)

// sameTimes reports whether the maps hold the same names with equal times.
func sameTimes(got, want map[string]time.Time) bool {
	if got == nil || len(got) != len(want) {
		return false
	}
	for name, at := range want {
		if !got[name].Equal(at) {
			return false
		}
	}
	return true
}

// started is the line busybox syslogd writes when it starts (the capture), at the time given.
func started(at string) string { return at + " host syslog.info syslogd started: BusyBox v1.37.0\n" }

// A system log without the start line of busybox syslogd (another syslog daemon, or one whose start has left the
// 4 MiB): a failure counts when its time is after the boot time, and byBoot says so.
func TestBootFailuresByBootTime(t *testing.T) {
	sys := newSystem(t)
	sys.file(syslogPath, `Dec 31 22:00:00 host daemon.err old[4]: ERROR: old failed to start
Dec 31 23:59:00 host daemon.err t-fail[1]: ERROR: t-fail failed to start
Jan  1 00:00:05 host daemon.err web[2]: ERROR: web failed to start
Jan  1 00:10:00 host daemon.err web[3]: ERROR: web failed to start
Jan  1 00:05:00 host daemon.err web[9]: ERROR: web failed to start
Jan  1 00:20:00 host daemon.err t-needfail[5]: ERROR: cannot start t-needfail as t-fail would not start
Jan  1 00:20:01 host daemon.err db[6]: boom: port 80 in use
Jan  1 00:20:02 host user.notice root: ERROR: db failed to start
Jan  1 00:20:03 host daemon.err db[7]: ERROR: other failed to start
Jan  1 00:20:04 host daemon.err db[8]: ERROR: db failed to start soon
ERROR: db failed to start
Jan  2 10:00:01 host daemon.err late[10]: ERROR: late failed to start
Jan  2 09:00:00 host daemon.info syslogd: started: BusyBox v1.37.0
Jan  2 09:00:01 host user.notice root: syslogd started: BusyBox v1.37.0
`)
	// Booted on New Year's Eve, asked on January 2: a December line is of the year before. A line after now is of the
	// year before as well (late: before the boot then); so is old, before the boot. Of several failures the last
	// counts, whatever the order of the lines. Lines of other forms, also under another name than the one they name, do
	// not count, and only the start line of syslogd itself, in its own form, starts a boot.
	now := time.Date(2027, 1, 2, 10, 0, 0, 0, time.UTC)
	boot := time.Date(2026, 12, 31, 23, 0, 0, 0, time.UTC)
	want := map[string]time.Time{"t-fail": time.Date(2026, 12, 31, 23, 59, 0, 0, time.UTC),
		"web": time.Date(2027, 1, 1, 0, 10, 0, 0, time.UTC)}
	if got, byBoot, err := BootFailures(sys.root, boot, now); err != nil || !byBoot || !sameTimes(got, want) {
		t.Errorf("BootFailures = %v, by the boot time %v, %v\nwant %v by the boot time", got, byBoot, err, want)
	}

	// Only the last 4 MiB are scanned, of both files: messages takes them all here, and the start line at its beginning
	// is not among them.
	now = time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	f, err := os.Create(sys.path(syslogPath))
	if err != nil {
		t.Fatal(err)
	}
	for at, text := range map[int64]string{
		0: started("Oct 20 07:00:00") + "Oct 20 07:10:00 host daemon.err early[1]: ERROR: early failed to start\n",
		scanLimit + 1000: "\nOct 20 06:20:00 host daemon.err old[2]: ERROR: old failed to start\n" +
			"Oct 20 07:20:00 host daemon.err recent[3]: ERROR: recent failed to start\n",
	} {
		if _, err := f.WriteAt([]byte(text), at); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	want = map[string]time.Time{"recent": time.Date(2026, 10, 20, 7, 20, 0, 0, time.UTC)}
	if got, byBoot, err := BootFailures(sys.root, now.Add(-time.Hour), now); err != nil || !byBoot || !sameTimes(got, want) {
		t.Errorf("BootFailures of a long log = %v, by the boot time %v, %v\nwant %v by the boot time", got, byBoot, err, want)
	}
}

// busybox syslogd writes its start line at every boot, and the lines after it are of this boot whatever their time: a
// host without a real-time clock (a Raspberry Pi) logs the start of the boot with the clock of the last shutdown, or
// of 1970, until NTP sets it, so that its failures look older than now less the uptime. The boot time does not count
// then (byBoot is false).
func TestBootFailuresAfterSyslogStart(t *testing.T) {
	now := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name, rotated, current string
		want                   map[string]time.Time
	}{
		{"the capture", "", capture(t),
			map[string]time.Time{"t-fail": time.Date(2026, 10, 20, 7, 46, 19, 0, time.UTC)}},
		{"no clock at the boot: the lines are of 1970 (Jan 1, in the year of now)", "",
			started("Jan  1 00:00:04") +
				"Jan  1 00:00:12 host daemon.err t-fail[302]: ERROR: t-fail failed to start\n" +
				"Oct 20 07:59:00 host daemon.info chronyd[400]: System clock wrong by 25000000 seconds\n",
			map[string]time.Time{"t-fail": time.Date(2026, 1, 1, 0, 0, 12, 0, time.UTC)}},
		{"a failure before the last start line is of a boot before, whatever its time", "",
			started("Oct 20 06:50:00") +
				"Oct 20 07:10:00 host daemon.err web[2]: ERROR: web failed to start\n" +
				started("Oct 17 21:00:05") +
				"Oct 17 21:00:09 host daemon.err db[3]: ERROR: db failed to start\n",
			map[string]time.Time{"db": time.Date(2026, 10, 17, 21, 0, 9, 0, time.UTC)}},
		{"the start line in messages.0, failures in both files",
			started("Oct 20 07:00:02") + "Oct 20 07:00:09 host daemon.err web[90]: ERROR: web failed to start\n",
			"Oct 20 07:30:00 host daemon.err db[91]: ERROR: db failed to start\n",
			map[string]time.Time{"web": time.Date(2026, 10, 20, 7, 0, 9, 0, time.UTC),
				"db": time.Date(2026, 10, 20, 7, 30, 0, 0, time.UTC)}},
		{"a start line without failures after it", "",
			"Oct 20 07:10:00 host daemon.err web[2]: ERROR: web failed to start\n" + started("Oct 20 07:20:00"),
			map[string]time.Time{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			sys := newSystem(t)
			if c.rotated != "" {
				sys.file(syslogPath+rotated, c.rotated)
			}
			sys.file(syslogPath, c.current)
			// the boot time an hour ago, and one after every line: neither counts
			for _, boot := range []time.Time{now.Add(-time.Hour), now.Add(-time.Minute)} {
				if got, byBoot, err := BootFailures(sys.root, boot, now); err != nil || byBoot || !sameTimes(got, c.want) {
					t.Errorf("booted at %v: BootFailures = %v, by the boot time %v, %v\nwant %v after the start line",
						boot, got, byBoot, err, c.want)
				}
			}
		})
	}
}

// Without a system log that can be read the caller shows less: no failures, no error.
func TestBootFailuresWithoutLog(t *testing.T) {
	now := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	if got, _, err := BootFailures(newSystem(t).root, now.Add(-time.Hour), now); err != nil || !sameTimes(got, nil) {
		t.Errorf("BootFailures without /var/log/messages = %v, %v, want none", got, err)
	}
	if os.Geteuid() != 0 { // root reads a file of mode 0000
		sys := newSystem(t)
		sys.file(syslogPath, capture(t))
		sys.lock(syslogPath)
		if got, _, err := BootFailures(sys.root, now.Add(-time.Hour), now); err != nil || !sameTimes(got, nil) {
			t.Errorf("BootFailures of an unreadable log = %v, %v, want none", got, err)
		}
	}
}
