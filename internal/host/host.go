// Package host reads the facts the extension needs from the running system: the mounts, the Alpine mode (sys,
// diskless, data) and the time of the boot. Every path is below Root, so tests run against a temp tree.
package host

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// FS reads the host below Root ("/" in production).
type FS struct{ Root string }

func (f FS) path(p string) string { return filepath.Join(f.Root, p) }

// Mode is how Alpine runs: installed to disk (sys), from RAM (diskless) or from RAM with /var on disk (data).
type Mode string

const (
	Sys      Mode = "sys"
	Diskless Mode = "diskless"
	Data     Mode = "data"
)

// Mount is a line of /proc/mounts.
type Mount struct {
	Device  string
	Point   string
	Type    string
	Options []string
}

// Mounts reads /proc/mounts. The kernel writes a space, tab, newline and backslash in a device or mount point as the
// octal escapes \040, \011, \012 and \134; they are decoded.
func (f FS) Mounts() ([]Mount, error) {
	data, err := os.ReadFile(f.path("proc/mounts"))
	if err != nil {
		return nil, err
	}
	var out []Mount
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		m := Mount{Device: unescape(fields[0]), Point: unescape(fields[1]), Type: fields[2]}
		if len(fields) > 3 {
			m.Options = strings.Split(fields[3], ",")
		}
		out = append(out, m)
	}
	return out, nil
}

// unescape decodes the octal escapes of the proc files (\040 for a space). Anything that is not a backslash and three
// octal digits of a byte stays as it is.
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func find(ms []Mount, point string) *Mount {
	var found *Mount
	for i := range ms {
		if ms[i].Point == point {
			found = &ms[i] // the last one wins, as in the kernel's view
		}
	}
	return found
}

// Mode tells sys, diskless and data apart: a tmpfs / means RAM, then /var on a block device means data.
func (f FS) Mode() (Mode, error) {
	ms, err := f.Mounts()
	if err != nil {
		return "", err
	}
	if root := find(ms, "/"); root == nil || root.Type != "tmpfs" {
		return Sys, nil
	}
	if v := find(ms, "/var"); v != nil && strings.HasPrefix(v.Device, "/dev/") {
		return Data, nil
	}
	return Diskless, nil
}

// uptime is the first field of /proc/uptime: the seconds since the boot, with their fraction ("12345.67").
var uptime = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

// maxUptime is the longest uptime BootTime takes, in seconds: the longest time.Duration.
const maxUptime = float64(math.MaxInt64 / int64(time.Second))

// BootTime is when the system booted: now less its uptime, the first field of /proc/uptime.
func (f FS) BootTime(now time.Time) (time.Time, error) {
	data, err := os.ReadFile(f.path("proc/uptime"))
	if err != nil {
		return time.Time{}, err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 || !uptime.MatchString(fields[0]) {
		return time.Time{}, fmt.Errorf("host: /proc/uptime holds no uptime: %q", data)
	}
	seconds, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || seconds >= maxUptime {
		return time.Time{}, fmt.Errorf("host: /proc/uptime holds no uptime: %q", data)
	}
	return now.Add(-time.Duration(math.Round(seconds * float64(time.Second)))), nil
}
