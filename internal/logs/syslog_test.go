package logs

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// capture is the system log of the capture, internal/openrc/testdata/messages.txt (its README tells how it was made).
func capture(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../openrc/testdata/messages.txt")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// lines are the lines of text, which ends with a newline.
func lines(text string) []string { return strings.Split(strings.TrimSuffix(text, "\n"), "\n") }

func TestSyslogProgram(t *testing.T) {
	var got []string
	for _, line := range lines(capture(t)) {
		got = append(got, SyslogProgram(line))
	}
	want := []string{"syslogd", "supervise-daemon", "supervise-daemon", "t-daemon", "t-fail", "root", "t-fail", "t-fail"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("programs of the capture\n got %q\nwant %q", got, want)
	}
	for line, want := range map[string]string{
		"Oct  5 07:46:19 alpine daemon.err sshd[12]: a day below 10": "sshd",
		"Oct 20 07:46:19 alpine local0.info my.app: dots in the tag": "my.app",
		"Oct 20 07:46:19 alpine <13> word":                           "word", // a priority syslogd has no name for
		"Oct 20 07:46:19 alpine daemon.err [12]: no tag":             "",
		"Oct 20 07:46:19 t-daemon: without host and priority (-S)":   "",
		"tick": "",
		"":     "",
	} {
		if got := SyslogProgram(line); got != want {
			t.Errorf("SyslogProgram(%q) = %q, want %q", line, got, want)
		}
	}
}

func TestSyslogFilter(t *testing.T) {
	sys := newSystem(t)
	captured := lines(capture(t))
	sys.file(syslogPath, capture(t))
	for _, tc := range []struct {
		service  string
		programs []string // as openrc.Reader.Programs has them
		want     []string
	}{
		{"t-fail", []string{"t-fail"}, []string{captured[4], captured[6], captured[7]}},
		// supervise-daemon logs under its own name; the message names the service or its daemon's program.
		{"t-super", []string{"sleep", "t-super"}, []string{captured[1], captured[2]}},
		// t-daemon runs /bin/sleep as well: the line of t-super's child names only the program, which cannot tell them
		// apart.
		{"t-daemon", []string{"sleep", "t-daemon"}, []string{captured[2], captured[3]}},
		// sh runs all kinds of services.
		{"t-log", []string{"sh", "t-log"}, []string{}},
	} {
		got, err := Tail(sys.root, syslogSource(tc.service), tc.programs, 500)
		if want := (Lines{Lines: tc.want, Offset: int64(len(capture(t)))}); err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("Tail of %s\n got %q, %v\nwant %q", tc.service, got.Lines, err, want.Lines)
		}
	}

	// The other messages of supervise-daemon, as they were seen while the capture was explored (a child killed and
	// started again, then stopped): those about its program count; those that name none do not, nor the supervisor
	// command line of another service that runs the same program. The last supervise-daemon line is made up: the same
	// message for a command given without its path.
	other := lines(`Oct 20 07:31:17 a98abb194600 daemon.warn supervise-daemon[187]: /bin/sleep, pid 188, terminated by signal 9
Oct 20 07:31:19 a98abb194600 daemon.info supervise-daemon[685]: Supervisor command line: supervise-daemon t-other --start --respawn-delay 2 --respawn-max 5 --respawn-period 1800 /bin/sleep -- 100000
Oct 20 07:31:19 a98abb194600 daemon.debug start-stop-daemon[502]: Will stop /bin/sleep
Oct 20 07:31:21 a98abb194600 daemon.info supervise-daemon[187]: stopping /bin/sleep, pid 476
Oct 20 07:31:21 a98abb194600 daemon.debug supervise-daemon[187]: Will stop PID 476
Oct 20 07:31:21 a98abb194600 daemon.debug supervise-daemon[187]: Sending signal 15 to PID 476
Oct 20 07:31:22 a98abb194600 daemon.info sh[901]: a line of a shell
Oct 20 07:31:23 a98abb194600 daemon.info supervise-daemon[187]: stopping sleep, pid 477
`)
	sys.file(syslogPath, strings.Join(other, "\n")+"\n")
	want := []string{other[0], other[3], other[7]}
	if got, err := Tail(sys.root, syslogSource("t-super"), []string{"sh", "sleep", "t-super"}, 500); err != nil ||
		!reflect.DeepEqual(got.Lines, want) {
		t.Errorf("Tail of t-super\n got %q, %v\nwant %q", got.Lines, err, want)
	}
}
