package host_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/host"
)

// fsOf writes the content of /proc/mounts into a temp root and returns the FS that reads it.
func fsOf(t *testing.T, mounts string) host.FS {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "proc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "proc/mounts"), []byte(mounts), 0o644); err != nil {
		t.Fatal(err)
	}
	return host.FS{Root: root}
}

// lines joins the lines of a proc file, with the final newline.
func lines(l ...string) string { return strings.Join(l, "\n") + "\n" }

func TestMountsEscapes(t *testing.T) {
	f := fsOf(t, lines(
		`/dev/sda2 / ext4 rw,relatime 0 0`,
		`/dev/sde1 /media/USB\040STICK vfat rw,relatime,fmask=0022 0 0`,
		`/dev/sde2 /media/tab\011here vfat rw 0 0`,
		`/dev/sde3 /media/new\012line vfat rw 0 0`,
		`/dev/sde4 /media/back\134slash vfat rw 0 0`,
		`/dev/mapper/my\040vol /mnt/my\040vol ext4 rw,noatime 0 0`,
		`/dev/sde5 /media/odd\04 vfat rw 0 0`,  // a truncated escape stays as it is
		`/dev/sde6 /media/odd\777 vfat rw 0 0`, // not a byte: stays as it is
		``,
		`tmpfs /run tmpfs rw,nosuid,nodev,size=102400k,mode=755 0 0`,
	))
	got, err := f.Mounts()
	if err != nil {
		t.Fatalf("Mounts: %v", err)
	}
	want := []host.Mount{
		{Device: "/dev/sda2", Point: "/", Type: "ext4", Options: []string{"rw", "relatime"}},
		{Device: "/dev/sde1", Point: "/media/USB STICK", Type: "vfat", Options: []string{"rw", "relatime", "fmask=0022"}},
		{Device: "/dev/sde2", Point: "/media/tab\there", Type: "vfat", Options: []string{"rw"}},
		{Device: "/dev/sde3", Point: "/media/new\nline", Type: "vfat", Options: []string{"rw"}},
		{Device: "/dev/sde4", Point: `/media/back\slash`, Type: "vfat", Options: []string{"rw"}},
		{Device: "/dev/mapper/my vol", Point: "/mnt/my vol", Type: "ext4", Options: []string{"rw", "noatime"}},
		{Device: "/dev/sde5", Point: `/media/odd\04`, Type: "vfat", Options: []string{"rw"}},
		{Device: "/dev/sde6", Point: `/media/odd\777`, Type: "vfat", Options: []string{"rw"}},
		{Device: "tmpfs", Point: "/run", Type: "tmpfs", Options: []string{"rw", "nosuid", "nodev", "size=102400k", "mode=755"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Mounts:\n got %#v\nwant %#v", got, want)
	}
}

func TestMountsMissing(t *testing.T) {
	if _, err := (host.FS{Root: t.TempDir()}).Mounts(); err == nil {
		t.Error("Mounts without proc/mounts: want an error")
	}
}

func TestMode(t *testing.T) {
	for _, c := range []struct {
		name, mounts string
		want         host.Mode
	}{
		{"sys", lines(`/dev/sda2 / ext4 rw 0 0`, `/dev/sda1 /boot ext4 rw 0 0`), host.Sys},
		{"no root line", lines(`proc /proc proc rw 0 0`), host.Sys},
		{"diskless", lines(`tmpfs / tmpfs rw 0 0`, `/dev/sdb1 /media/sdb1 vfat rw 0 0`), host.Diskless},
		{"diskless, /var in RAM", lines(`tmpfs / tmpfs rw 0 0`, `tmpfs /var tmpfs rw 0 0`), host.Diskless},
		{"data", lines(`tmpfs / tmpfs rw 0 0`, `/dev/sda3 /var ext4 rw 0 0`), host.Data},
		{"data, the last / wins", lines(`/dev/sda2 / ext4 rw 0 0`, `tmpfs / tmpfs rw 0 0`, `/dev/sda3 /var ext4 rw 0 0`), host.Data},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := fsOf(t, c.mounts).Mode()
			if err != nil || got != c.want {
				t.Errorf("Mode = %q, %v; want %q", got, err, c.want)
			}
		})
	}
	if _, err := (host.FS{Root: t.TempDir()}).Mode(); err == nil {
		t.Error("Mode without proc/mounts: want an error")
	}
}

// uptimeOf writes the content of /proc/uptime into a temp root and returns the FS that reads it.
func uptimeOf(t *testing.T, uptime string) host.FS {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "proc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "proc/uptime"), []byte(uptime), 0o644); err != nil {
		t.Fatal(err)
	}
	return host.FS{Root: root}
}

func TestBootTime(t *testing.T) {
	now := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		uptime string
		want   time.Time
	}{
		{"12345.67 54321.00\n", now.Add(-12345670 * time.Millisecond)},
		{"3600.00 7000.00\n", time.Date(2026, 10, 20, 7, 0, 0, 0, time.UTC)},
		{"0.01 0.00", now.Add(-10 * time.Millisecond)},
		{"2.01 3.00\n", now.Add(-2010 * time.Millisecond)}, // 2.01 * 1e9 is 2009999999.9999998 as a float
		{"42 1\n", now.Add(-42 * time.Second)},
	} {
		got, err := uptimeOf(t, c.uptime).BootTime(now)
		if err != nil || !got.Equal(c.want) {
			t.Errorf("BootTime with /proc/uptime %q = %v, %v; want %v", c.uptime, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "\n", "up 5 min", "-5.00 1.00", "NaN 1", "Inf 1", "1e3 1", "0x10 1", "1,5 1",
		"99999999999999999999.00 1"} {
		if got, err := uptimeOf(t, bad).BootTime(now); err == nil {
			t.Errorf("BootTime with /proc/uptime %q = %v; want an error", bad, got)
		}
	}
	if got, err := (host.FS{Root: t.TempDir()}).BootTime(now); err == nil {
		t.Errorf("BootTime without /proc/uptime = %v; want an error", got)
	}
}
