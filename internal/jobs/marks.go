package jobs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/openrc"
)

// DefaultMarkDir is where the markers of the Executor are (below its Root): next to the job store, in /run.
const DefaultMarkDir = "/run/edelweiss-ext-openrc"

// The directories of the markers below the mark directory: the services that were started or stopped well, and the
// services whose last start failed.
const (
	okDir     = "ok"
	failedDir = "failed"
)

// markBase is the directory of the markers: dir (DefaultMarkDir when empty) below root.
func markBase(root, dir string) string {
	if dir == "" {
		dir = DefaultMarkDir
	}
	return filepath.Join(root, dir)
}

// mark leaves the marker for the outcome of the step s, whose command worked or not (ok); see Executor. The step has
// passed checkMark.
func (x Executor) mark(s Step, ok bool) {
	if s.Mark == "" {
		return
	}
	base := markBase(x.Root, x.MarkDir)
	var err error
	switch {
	case ok:
		err = errors.Join(removeMark(filepath.Join(base, failedDir, s.Service)), writeMark(filepath.Join(base, okDir), s.Service))
	case s.Mark == MarkStart:
		err = writeMark(filepath.Join(base, failedDir), s.Service)
	}
	if err != nil && x.Note != nil {
		x.Note("the marker of %s: %v", s.Service, err)
	}
}

// writeMark writes the marker of the service in dir (made 0755 if it is missing): now as RFC 3339 in UTC, in a file
// 0644 that appears whole.
func writeMark(dir, service string) error {
	if err := mkdirs(dir); err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, service), []byte(time.Now().UTC().Format(time.RFC3339)), 0o644)
}

// removeMark removes the marker; a marker that is not there is what the caller wants.
func removeMark(path string) error {
	if err := os.Remove(path); !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// ReadMarks reads the markers an Executor with this Root and MarkDir (DefaultMarkDir when empty) wrote: for each
// service the time its last start failed, and the time of its last start or stop that worked. A directory that is
// missing has no markers (empty maps, not nil). A file that is no marker is skipped: one whose name is no service
// name (a marker that is being written), one that is not a regular file, one that does not hold a time.
func ReadMarks(root, dir string) (failed, ok map[string]time.Time, err error) {
	base := markBase(root, dir)
	if failed, err = readMarkDir(filepath.Join(base, failedDir)); err != nil {
		return nil, nil, err
	}
	if ok, err = readMarkDir(filepath.Join(base, okDir)); err != nil {
		return nil, nil, err
	}
	return failed, ok, nil
}

func readMarkDir(dir string) (map[string]time.Time, error) {
	marks := map[string]time.Time{}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return marks, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.Type().IsRegular() || !openrc.ValidName(e.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if errors.Is(err, fs.ErrNotExist) { // removed since the listing
			continue
		}
		if err != nil {
			return nil, err
		}
		if at, err := time.Parse(time.RFC3339, strings.TrimSpace(string(data))); err == nil {
			marks[e.Name()] = at
		}
	}
	return marks, nil
}
