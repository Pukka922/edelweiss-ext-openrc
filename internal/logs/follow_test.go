package logs

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// following runs Follow of src from offset in the background, checking every few milliseconds, until the test ends.
// Its events arrive on the channel.
func following(t *testing.T, sys *system, src Source, programs []string, offset int64) <-chan Event {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan Event, 100)
	done := make(chan struct{})
	go func() {
		defer close(done)
		Follow(ctx, sys.root, src, programs, offset, 5*time.Millisecond, func(e Event) bool {
			select {
			case events <- e:
				return true
			case <-ctx.Done():
				return false
			}
		})
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return events
}

// expect fails the test unless the next events are want, each within a few seconds.
func expect(t *testing.T, events <-chan Event, want ...Event) {
	t.Helper()
	for _, w := range want {
		select {
		case e := <-events:
			if !reflect.DeepEqual(e, w) {
				t.Fatalf("event %+v, want %+v", e, w)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("no event, want %+v", w)
		}
	}
}

// quiet fails the test when an event comes within a few checks.
func quiet(t *testing.T, events <-chan Event) {
	t.Helper()
	select {
	case e := <-events:
		t.Fatalf("event %+v, want none", e)
	case <-time.After(50 * time.Millisecond):
	}
}

func line(text string) Event { return Event{Kind: "line", Line: text} }

var reset = Event{Kind: "reset"}

func TestFollowAppend(t *testing.T) {
	sys := newSystem(t)
	sys.file("/var/log/app.log", "one\n")
	src := fileSource("/var/log/app.log")
	tail, err := Tail(sys.root, src, nil, 500)
	if err != nil {
		t.Fatal(err)
	}
	events := following(t, sys, src, nil, tail.Offset)
	quiet(t, events) // what Tail had comes no second time
	sys.add("/var/log/app.log", "two\nthr")
	expect(t, events, line("two"))
	quiet(t, events) // a line counts once its newline is there
	sys.add("/var/log/app.log", "ee\n\nfour\n")
	expect(t, events, line("three"), line(""), line("four"))
	// A line that grows to 4 MiB without its newline is not waited for any longer.
	sys.add("/var/log/app.log", strings.Repeat("x", scanLimit))
	select {
	case e := <-events:
		if e.Kind != "line" || len(e.Line) != scanLimit || strings.Trim(e.Line, "x") != "" {
			t.Fatalf("event %q of %d bytes, want the line of 4 MiB", e.Kind, len(e.Line))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no event, want the line of 4 MiB")
	}
	sys.add("/var/log/app.log", "end\n")
	expect(t, events, line("end"))

	// The system log: only the lines of the programs.
	sys.file(syslogPath, capture(t))
	src = syslogSource("t-fail")
	if tail, err = Tail(sys.root, src, []string{"t-fail"}, 500); err != nil {
		t.Fatal(err)
	}
	events = following(t, sys, src, []string{"t-fail"}, tail.Offset)
	failed := "Oct 20 07:50:01 host daemon.err t-fail[400]: ERROR: t-fail failed to start"
	sys.add(syslogPath, "Oct 20 07:50:00 host user.notice root: not of t-fail\n"+failed+"\n")
	expect(t, events, line(failed))
	quiet(t, events)
}

func TestFollowStops(t *testing.T) {
	sys := newSystem(t)
	sys.file("/var/log/app.log", "one\ntwo\n")
	// emit wants no more after the first line.
	var got []Event
	done := make(chan struct{})
	go func() {
		defer close(done)
		Follow(context.Background(), sys.root, fileSource("/var/log/app.log"), nil, 0, time.Millisecond, func(e Event) bool {
			got = append(got, e)
			return false
		})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Follow goes on after emit returned false")
	}
	if want := []Event{line("one")}; !reflect.DeepEqual(got, want) {
		t.Errorf("events %+v, want %+v", got, want)
	}

	// The context ends, while Follow waits for a file that is not there.
	ctx, cancel := context.WithCancel(context.Background())
	done = make(chan struct{})
	go func() {
		defer close(done)
		Follow(ctx, sys.root, fileSource("/var/log/none.log"), nil, 0, time.Millisecond, func(Event) bool { return true })
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Follow goes on after the context ended")
	}
}

func TestFollowRotation(t *testing.T) {
	sys := newSystem(t)
	// Truncated (logrotate's copytruncate): a reset, then the file from its start.
	sys.file("/var/log/app.log", "a\nb\n")
	events := following(t, sys, fileSource("/var/log/app.log"), nil, 4)
	quiet(t, events)
	if err := os.Truncate(sys.path("/var/log/app.log"), 0); err != nil {
		t.Fatal(err)
	}
	expect(t, events, reset)
	sys.add("/var/log/app.log", "c\n")
	expect(t, events, line("c"))
	quiet(t, events)

	// Renamed (logrotate's create mode, busybox syslogd's rotation to messages.0): while no file is at the path, the
	// renamed one is read on, its writer may still write to it.
	sys.file("/var/log/svc.log", "a\n")
	events = following(t, sys, fileSource("/var/log/svc.log"), nil, 2)
	quiet(t, events)
	if err := os.Rename(sys.path("/var/log/svc.log"), sys.path("/var/log/svc.log.1")); err != nil {
		t.Fatal(err)
	}
	sys.add("/var/log/svc.log.1", "still the old one\n")
	expect(t, events, line("still the old one"))
	quiet(t, events)
	// What was written to it before the new file at the path is seen comes first, its last line also without a newline
	// (as tail -F does); then a reset and the new file from its start, also when it is longer than what was read of the
	// old one.
	sys.add("/var/log/svc.log.1", "last\nno newline")
	sys.file("/var/log/svc.log", "new file\nsecond\n")
	expect(t, events, line("last"), line("no newline"), reset, line("new file"), line("second"))
	quiet(t, events)

	// Smaller than the offset when Follow starts: it changed since Tail.
	sys.file("/var/log/short.log", "x\n")
	expect(t, following(t, sys, fileSource("/var/log/short.log"), nil, 100), reset, line("x"))

	// Not there yet when Follow starts.
	events = following(t, sys, fileSource("/var/log/later.log"), nil, 0)
	quiet(t, events)
	sys.file("/var/log/later.log", "here\n")
	expect(t, events, line("here"))
}
