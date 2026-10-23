package logs

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
)

// scanLimit is how much of the end of a log is read at most, 4 MiB: the system log can be large.
const scanLimit = 4 << 20

// rotated is the suffix of the file busybox syslogd rotates the system log to (at 200 KiB by default, so the current
// file is often short).
const rotated = ".0"

// Lines are the last lines of a source.
type Lines struct {
	Lines  []string `json:"lines"`
	Offset int64    `json:"offset"` // file size the lines end at (for follow)
	Capped bool     `json:"capped"` // only the last 4 MiB were scanned, and they held fewer lines (syslog: of the service)
}

// Tail is the last n lines of the source (of the system log: those of programs, see matcher), the last one also
// without a final newline. It reads the last 4 MiB of the source at most (of the system log: of messages.0 and
// messages together, see sourceLines): Capped tells that it found fewer than n lines there while there is more before
// them. Offset is the size of the file the lines end in, where Follow goes on. The file has to be one open opens (a
// regular file below /var/log); other errors are those of reading it.
func Tail(root string, src Source, programs []string, n int) (Lines, error) {
	f, _, err := open(root, src.Path)
	if err != nil {
		return Lines{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return Lines{}, err
	}
	all, cut, err := sourceLines(root, src, f, st.Size())
	if err != nil {
		return Lines{}, err
	}
	keep := filter(src, programs)
	lines := []string{}
	for _, l := range all {
		if keep(l) {
			lines = append(lines, l)
		}
	}
	n = max(n, 0)
	return Lines{Lines: lines[max(len(lines)-n, 0):], Offset: st.Size(), Capped: cut && len(lines) < n}, nil
}

// filter tells the lines of the source that are its own: of the system log those of programs (matcher), of a file all.
func filter(src Source, programs []string) func(line string) bool {
	if src.ID != syslogID {
		return func(string) bool { return true }
	}
	return matcher(programs).match
}

// sourceLines are the lines in the last 4 MiB of the source, whose file f is size bytes long (see lastLines). Of the
// system log they are those of the file syslogd rotated it to (messages.0, when there is one that open opens),
// followed by those of messages, in 4 MiB from the end of both. cut tells that there is more before them.
func sourceLines(root string, src Source, f *os.File, size int64) (lines []string, cut bool, err error) {
	lines, cut, err = lastLines(f, size, scanLimit)
	if err != nil || cut || src.ID != syslogID {
		return lines, cut, err
	}
	old, _, err := open(root, src.Path+rotated)
	if err != nil {
		return lines, false, nil // none
	}
	defer old.Close()
	st, err := old.Stat()
	if err != nil {
		return nil, false, err
	}
	before, cut, err := lastLines(old, st.Size(), scanLimit-size)
	if err != nil {
		return nil, false, err
	}
	return append(before, lines...), cut, nil
}

// lastLines are the lines in the last limit bytes of the file, which is size bytes long: the line the limit cuts is
// left out, the last one counts also without a final newline. cut tells that the file holds more before them.
func lastLines(f *os.File, size, limit int64) (lines []string, cut bool, err error) {
	start := max(size-limit, 0)
	buf := make([]byte, size-start)
	n, err := f.ReadAt(buf, start)
	if err != nil && !errors.Is(err, io.EOF) { // EOF: the file shrank since its size was taken
		return nil, false, err
	}
	buf = buf[:n]
	if start > 0 {
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		} else {
			buf = nil // all of it is the end of one line
		}
	}
	return split(buf), start > 0, nil
}

// split is the lines of b; the last one counts also without a final newline.
func split(b []byte) []string {
	if len(b) == 0 {
		return []string{}
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}
