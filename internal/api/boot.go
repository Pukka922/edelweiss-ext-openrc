package api

import (
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/logs"
)

// The files of the system log that logs.BootFailures reads, below the root.
const (
	systemLog  = "/var/log/messages"
	rotatedLog = "/var/log/messages.0"
)

// bootDrift is how far the boot time may move before a scan that it decided is made again. Now less the uptime is read
// at another moment for every view, so it differs by a fraction of a second, and NTP slews the clock; the lines of the
// log have whole seconds.
const bootDrift = time.Second

// bootCache keeps the last scan of the system log for the failed starts since the boot, with the files of the log it
// was made for, and the boot time it was made with when that decided (see logs.BootFailures).
type bootCache struct {
	mu     sync.Mutex
	key    bootKey
	byBoot bool
	boot   time.Time
	failed map[string]time.Time // nil while nothing is kept
}

// bootKey is what a scan depends on besides the boot time: the two files of the system log as they were.
type bootKey struct {
	rotated, current fileKey
}

// fileKey is a file as stat sees it; the zero value is a file that is not there (or cannot be looked at).
type fileKey struct {
	there    bool
	dev, ino uint64
	size     int64
	mtime    int64 // Unix nanoseconds
}

func fileKeyOf(p string) fileKey {
	st, err := os.Stat(p)
	if err != nil {
		return fileKey{}
	}
	k := fileKey{there: true, size: st.Size(), mtime: st.ModTime().UnixNano()}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		k.dev, k.ino = uint64(sys.Dev), sys.Ino
	}
	return k
}

// bootFailures are the services whose start failed since the boot (logs.BootFailures, or scanBoot when a test set it).
// The view is asked for every 5 s and a scan reads up to 4 MiB of the system log, so the last one is kept for as long
// as both files of the log are as they were, and, when the boot time decided the scan (the log holds no start line of
// syslogd), as long as the boot time stays within bootDrift of the one it was made with; a change of any of them scans
// anew. The files are looked at before the scan: one that changes during it is scanned again at the next view. now
// only places the lines, which have no year, in one: a line that is there was written before the scan, and a later now
// places it alike. A scan that fails is not kept. One scan runs at a time, and the views wait for it.
func (a *API) bootFailures(boot, now time.Time) (map[string]time.Time, error) {
	key := bootKey{rotated: fileKeyOf(filepath.Join(a.Root, rotatedLog)),
		current: fileKeyOf(filepath.Join(a.Root, systemLog))}
	c := &a.boot
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failed != nil && c.key == key && (!c.byBoot || (boot.Sub(c.boot).Abs() < bootDrift)) {
		return c.failed, nil
	}
	scan := a.scanBoot
	if scan == nil {
		scan = logs.BootFailures
	}
	failed, byBoot, err := scan(a.Root, boot, now)
	if err != nil {
		return nil, err
	}
	c.key, c.byBoot, c.boot, c.failed = key, byBoot, boot, failed
	return failed, nil
}
