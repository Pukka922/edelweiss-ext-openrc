package logs

import (
	"bytes"
	"context"
	"os"
	"time"
)

// Event is what Follow sees: a new line (Kind "line"), or that the file shrank or was replaced (Kind "reset": the lines
// that follow are those of the file from its start).
type Event struct {
	Kind string
	Line string
}

// readChunk is how much Follow reads at once.
const readChunk = 64 << 10

// Follow emits the lines added to the source after offset (of the system log: those of programs, as Tail), checking
// every interval, until ctx ends or emit returns false. A line is emitted once its newline is there (one that grows to
// scanLimit without one is emitted as it is). A file smaller than what was read emits a reset and is read from its
// start. Another file at the path (the log was rotated: logrotate, busybox syslogd's messages.0) is switched to as
// tail -F does: the rest of the old one first, then a reset and the new one from its start. While there is no file at
// the path, Follow goes on with the one it reads (its writer may still write to it); before it had one, it waits for
// it. The system log is followed in /var/log/messages alone.
func Follow(ctx context.Context, root string, src Source, programs []string, offset int64, every time.Duration, emit func(Event) bool) {
	fl := follower{root: root, path: src.Path, keep: filter(src, programs), emit: emit, offset: max(offset, 0)}
	defer fl.close()
	tick := time.NewTicker(every)
	defer tick.Stop()
	for fl.check(ctx) {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// follower is the state of Follow.
type follower struct {
	root, path string
	keep       func(line string) bool
	emit       func(Event) bool
	f          *os.File // the file read, nil before there was one
	offset     int64    // how much of f was read
	partial    []byte   // the start of a line whose newline is not there yet
	buf        []byte
}

// check emits what was added to the file since the last check; false when emit wants no more.
func (fl *follower) check(ctx context.Context) bool {
	f, _, err := open(fl.root, fl.path)
	switch {
	case err != nil && fl.f == nil:
		return true // not there yet
	case err != nil: // not there for now: go on with the file read so far
	case fl.f == nil:
		fl.f = f
	case same(f, fl.f):
		f.Close()
	default: // another file at the path: first the rest of the one read so far, as tail -F does
		if !fl.drain(ctx) {
			f.Close()
			return false
		}
		fl.f.Close()
		fl.f = f
		if !fl.reset() {
			return false
		}
	}
	st, err := fl.f.Stat()
	if err != nil {
		return true
	}
	if st.Size() < fl.offset { // it shrank
		if !fl.reset() {
			return false
		}
	}
	return fl.read(ctx, st.Size())
}

// drain emits the rest of the file read so far, which another file at the path took the place of: what was written to
// it since the last check, its last line also without a newline.
func (fl *follower) drain(ctx context.Context) bool {
	if st, err := fl.f.Stat(); err == nil && st.Size() >= fl.offset && !fl.read(ctx, st.Size()) {
		return false
	}
	return len(fl.partial) == 0 || fl.line(fl.take(nil))
}

// same reports whether the open files are one file.
func same(a, b *os.File) bool {
	sa, errA := a.Stat()
	sb, errB := b.Stat()
	return errA == nil && errB == nil && os.SameFile(sa, sb)
}

// reset starts again at the start of the file.
func (fl *follower) reset() bool {
	fl.offset, fl.partial = 0, fl.partial[:0]
	return fl.emit(Event{Kind: "reset"})
}

// read emits the lines of the file from offset up to size.
func (fl *follower) read(ctx context.Context, size int64) bool {
	if fl.buf == nil {
		fl.buf = make([]byte, readChunk)
	}
	for fl.offset < size && ctx.Err() == nil {
		n, err := fl.f.ReadAt(fl.buf[:min(int64(len(fl.buf)), size-fl.offset)], fl.offset)
		fl.offset += int64(n)
		if !fl.lines(fl.buf[:n]) {
			return false
		}
		if err != nil { // the file shrank since its size was taken: the next check tells
			return true
		}
	}
	return true
}

// lines emits the lines that data, read after partial, completes.
func (fl *follower) lines(data []byte) bool {
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			fl.partial = append(fl.partial, data...)
			return len(fl.partial) < scanLimit || fl.line(fl.take(nil)) // too long to wait for its end
		}
		if !fl.line(fl.take(data[:i])) {
			return false
		}
		data = data[i+1:]
	}
	return true
}

// take is the line that partial and end make; partial is empty then.
func (fl *follower) take(end []byte) string {
	line := string(fl.partial) + string(end)
	fl.partial = fl.partial[:0]
	return line
}

// line emits the line when it is one of the source's; false when emit wants no more.
func (fl *follower) line(text string) bool {
	return !fl.keep(text) || fl.emit(Event{Kind: "line", Line: text})
}

// close closes the file read.
func (fl *follower) close() {
	if fl.f != nil {
		fl.f.Close()
	}
}
