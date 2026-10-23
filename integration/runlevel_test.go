//go:build integration

package integration

import (
	"os"
	"testing"
)

// TestRunlevelChange starts a runlevel that holds the failing t-fail, as a boot does, and writes to the test log what
// OpenRC marks: whether the failed start makes t-fail "failed" (rc-status-failed is the rc-status output after the first
// run, internal/openrc/testdata/rc-status-failed.txt), whether a second run retries it and whether a manual start clears
// the marker; then the same with the runlevels of a boot (sysinit, boot, default) and a service that needs t-fail. It
// starts in a fresh container: no service is running, the softlevel is empty.
func TestRunlevelChange(t *testing.T) {
	c := newCapture(t)
	openrc := findTool(t, "openrc")
	if err := os.Mkdir("/etc/runlevels/captest", 0o755); err != nil {
		t.Fatal(err)
	}
	c.step(c.update, "add", "t-fail", "captest")
	c.step(c.update, "add", "t-daemon", "captest")
	look := func(suffix string) {
		block("rc-status-failed"+suffix, c.rcStatus())
		block("ls-openrc"+suffix, c.sh("ls -la /run/openrc/failed /run/openrc/*"))
		out, got := c.run(c.service, "t-fail", "status")
		block("status-t-fail"+suffix, withStatus(out, got))
	}
	for _, suffix := range []string{"", "-2"} {
		out, got := c.run(openrc, "captest")
		block("openrc-captest"+suffix, withStatus(out, got))
		look(suffix)
	}
	block("softlevel", c.sh("cat /run/openrc/softlevel"))
	out, got := c.run(c.service, "t-fail", "start")
	block("start-fail-after-openrc", withStatus(out, got))
	look("-3")

	// A boot: sysinit, boot, default, with t-fail and a service that needs it in default.
	writeService(t, "t-needfail", "Test service that needs t-fail", "depend() {\n\tneed t-fail\n}\n")
	c.run(c.update, "-u")
	c.step(c.update, "add", "t-fail", "default")
	c.step(c.update, "add", "t-needfail", "default")
	c.sh(": >/run/openrc/softlevel")
	for _, runlevel := range []string{"sysinit", "boot", "default"} {
		out, got := c.run(openrc, runlevel)
		block("openrc-"+runlevel, withStatus(out, got))
	}
	look("-boot")
	c.finish()
}
