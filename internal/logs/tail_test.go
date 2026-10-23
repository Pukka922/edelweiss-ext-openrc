package logs

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

// fileSource is the source of the log file p.
func fileSource(p string) Source { return readable("file:"+strings.TrimPrefix(p, "/var/log/"), p) }

func TestTail(t *testing.T) {
	sys := newSystem(t)
	var many strings.Builder
	var last []string
	for i := 1; i <= 2000; i++ {
		fmt.Fprintf(&many, "line %d\n", i)
		if i > 1500 {
			last = append(last, fmt.Sprintf("line %d", i))
		}
	}
	sys.file("/var/log/many.log", many.String())
	open := "first\n\nlast, no newline"
	sys.file("/var/log/open.log", open)
	sys.file("/var/log/empty.log", "")
	sys.file("/var/log/two.log", "a\nb\n")
	// Only the last 4 MiB are read; the line the limit cuts is left out.
	long := "first\n" + strings.Repeat("x", scanLimit) + "\nlast\n"
	sys.file("/var/log/long.log", long)
	for _, tc := range []struct {
		file string
		n    int
		want Lines
	}{
		{"many.log", 500, Lines{Lines: last, Offset: int64(many.Len())}},
		{"open.log", 500, Lines{Lines: []string{"first", "", "last, no newline"}, Offset: int64(len(open))}},
		{"empty.log", 500, Lines{Lines: []string{}}},
		{"two.log", 1, Lines{Lines: []string{"b"}, Offset: 4}},
		{"two.log", 0, Lines{Lines: []string{}, Offset: 4}},
		{"long.log", 500, Lines{Lines: []string{"last"}, Offset: int64(len(long)), Capped: true}},
		{"long.log", 1, Lines{Lines: []string{"last"}, Offset: int64(len(long))}}, // the limit cut nothing it needed
	} {
		got, err := Tail(sys.root, fileSource("/var/log/"+tc.file), nil, tc.n)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Tail(%s, %d) = %.200v, %v\nwant %.200v", tc.file, tc.n, got, err, tc.want)
		}
	}

	// Nothing outside /var/log is read, not even through a link, nor anything that is not a regular file.
	sys.file("/etc/secret.log", "secret\n")
	sys.link("/var/log/escape.log", "../../etc/secret.log")
	sys.link("/var/log/abs.log", sys.path("/etc/secret.log"))
	sys.dir("/var/log/dir.log")
	if err := syscall.Mkfifo(sys.path("/var/log/fifo.log"), 0o644); err != nil { // opening it must not wait for a writer
		t.Fatal(err)
	}
	for _, p := range []string{"/etc/secret.log", "/var/log/../../etc/secret.log", "/var/log/escape.log", "/var/log/abs.log",
		"/var/log/dir.log", "/var/log/fifo.log", "/var/log/missing.log", "/var/log", "var/log/two.log"} {
		if got, err := Tail(sys.root, fileSource(p), nil, 500); err == nil {
			t.Errorf("Tail(%s) = %+v, want an error", p, got)
		}
	}
}

func TestSyslogTailOnly(t *testing.T) {
	sys := newSystem(t)
	const size = 200 << 20
	line := func(text string) string { return "Oct 20 07:46:19 host daemon.err t-fail[302]: " + text + "\n" }
	f, err := os.Create(sys.path(syslogPath))
	if err != nil {
		t.Fatal(err)
	}
	inside, end := line("inside the last 4 MiB"), line("at the end")
	cut := line("cut by the limit")
	for at, text := range map[int64]string{
		0:                                    line("at the start"),
		size - scanLimit - int64(len(cut)/2): cut, // starts before the last 4 MiB, ends in them
		size - scanLimit + 100:               "\n" + inside,
		size - int64(len(end)) - 1:           "\n" + end,
	} {
		if _, err := f.WriteAt([]byte(text), at); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	// The rest of the file is zeros: a sparse file, nothing in it is read but its last 4 MiB.
	if st, err := os.Stat(sys.path(syslogPath)); err != nil || st.Size() != size {
		t.Fatalf("the system log is %v bytes (%v), want %d", st.Size(), err, size)
	}
	src := syslogSource("t-fail")
	want := Lines{Lines: []string{strings.TrimSuffix(inside, "\n"), strings.TrimSuffix(end, "\n")}, Offset: size, Capped: true}
	if got, err := Tail(sys.root, src, []string{"t-fail"}, 500); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Tail = %+v, %v\nwant %+v", got, err, want)
	}
	// With enough lines in the last 4 MiB, what was not scanned does not matter.
	want = Lines{Lines: want.Lines[1:], Offset: size}
	if got, err := Tail(sys.root, src, []string{"t-fail"}, 1); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Tail of one line = %+v, %v\nwant %+v", got, err, want)
	}
}

func TestSyslogRotated(t *testing.T) {
	sys := newSystem(t)
	msg := func(text string) string { return "Oct 20 07:46:19 host daemon.err t-fail[302]: " + text }
	src := syslogSource("t-fail")
	// busybox syslogd rotated /var/log/messages to messages.0 (at 200 KiB by default): its lines come first.
	sys.file(syslogPath+".0", msg("rotated 1")+"\n"+msg("rotated 2")) // its last line without a newline
	current := msg("current") + "\n"
	sys.file(syslogPath, current)
	want := Lines{Lines: []string{msg("rotated 1"), msg("rotated 2"), msg("current")}, Offset: int64(len(current))}
	if got, err := Tail(sys.root, src, []string{"t-fail"}, 500); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Tail with messages.0\n got %+v, %v\nwant %+v", got, err, want)
	}
	// Following reads messages alone.
	expect(t, following(t, sys, src, []string{"t-fail"}, 0), line(msg("current")))

	// Both share the 4 MiB, taken from the end: of messages.0 only what messages leaves; the line the limit cuts is left
	// out.
	sys.file(syslogPath+".0", msg("too early")+"\n"+strings.Repeat("x", scanLimit)+"\n"+msg("rotated")+"\n")
	want = Lines{Lines: []string{msg("rotated"), msg("current")}, Offset: int64(len(current)), Capped: true}
	if got, err := Tail(sys.root, src, []string{"t-fail"}, 500); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Tail with a long messages.0\n got %.300q (offset %d, capped %v), %v\nwant %+v", got.Lines, got.Offset,
			got.Capped, err, want)
	}
	want = Lines{Lines: []string{msg("current")}, Offset: int64(len(current))}
	if got, err := Tail(sys.root, src, []string{"t-fail"}, 1); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Tail of one line\n got %+v, %v\nwant %+v", got, err, want)
	}
	// A long messages leaves less: here not even all of the one line of messages.0.
	sys.file(syslogPath+".0", msg("rotated")+"\n")
	long := strings.Repeat("y", scanLimit-100) + "\n" + current
	sys.file(syslogPath, long)
	if got, err := Tail(sys.root, src, []string{"t-fail"}, 500); err != nil ||
		!reflect.DeepEqual(got, Lines{Lines: []string{msg("current")}, Offset: int64(len(long)), Capped: true}) {
		t.Errorf("Tail with a long messages\n got %.300q (offset %d, capped %v), %v", got.Lines, got.Offset, got.Capped, err)
	}
	sys.file(syslogPath, current)
	// messages.0 is read below /var/log only; a file source of messages is that file alone.
	sys.file("/etc/secret", msg("secret")+"\n")
	if err := os.Remove(sys.path(syslogPath + ".0")); err != nil {
		t.Fatal(err)
	}
	sys.link(syslogPath+".0", "../../etc/secret")
	if got, err := Tail(sys.root, src, []string{"t-fail"}, 500); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Tail with messages.0 outside /var/log\n got %+v, %v\nwant %+v", got, err, want)
	}
	if err := os.Remove(sys.path(syslogPath + ".0")); err != nil {
		t.Fatal(err)
	}
	sys.file(syslogPath+".0", msg("rotated")+"\n")
	if got, err := Tail(sys.root, fileSource(syslogPath), nil, 500); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Tail of messages as a file\n got %+v, %v\nwant %+v", got, err, want)
	}
}
