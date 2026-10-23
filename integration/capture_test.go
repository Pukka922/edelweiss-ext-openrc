//go:build integration

package integration

import (
	"strings"
	"testing"
	"time"
)

// TestCapture starts the test services with the real OpenRC and writes the outputs the extension parses to the test log,
// each between "=== <name> ===" lines (internal/openrc/testdata/README.md says how they become testdata). It only checks
// that every command ran with the exit status expected; what the outputs look like is for the parsers' tests.
func TestCapture(t *testing.T) {
	c := newCapture(t)
	block("tools", c.sh("ls -l "+c.service+" "+c.update+" "+c.status+"; cat /etc/alpine-release; apk info -v openrc busybox; "+
		c.service+" --version; true"))

	for _, s := range []string{"t-daemon", "t-super", "t-oneshot", "t-needs", "t-log", "t-crash"} {
		block("start-"+s, c.step(c.service, s, "start"))
	}
	block("rc-update-add-t-daemon", c.step(c.update, "add", "t-daemon", "default"))
	block("rc-update-add-t-oneshot", c.step(c.update, "add", "t-oneshot", "boot"))
	// t-fail is in a runlevel as well, so that rc-status lists it while it is stopped.
	block("rc-update-add-t-fail", c.step(c.update, "add", "t-fail", "default"))

	// Lines for syslog, written by the test: the one of the service first; another facility and priority; no tag.
	c.sh(`logger -t t-daemon "hello from t-daemon"`)
	c.sh(`logger -p daemon.err -t t-fail "an error of t-fail"`)
	c.sh(`logger "no tag"`)

	// A daemon that died without OpenRC knowing: OpenRC reports it crashed.
	kill(t, readPID(t, "/run/t-crash.pid"))

	failOut, failStatus := c.run(c.service, "t-fail", "start")
	if failStatus == 0 {
		t.Error("rc-service t-fail start succeeded")
	}
	waitFor(t, "the lines are in /var/log/messages", func() bool { return fileHas("/var/log/messages", "t-fail failed to start") })
	waitFor(t, "t-log wrote its output", func() bool { return fileHas("/var/log/t-log.log", "tick") })
	block("messages", c.sh("tail -n 20 /var/log/messages"))
	time.Sleep(2 * time.Second) // so that the uptime of t-super in rc-status is not 00:00:00

	statusOut := c.rcStatus()
	block("rc-status-args", strings.Join(c.statusArgs, " ")+"\n")
	block("rc-status", statusOut)
	block("rc-update-show", c.step(c.update, "show", "--verbose"))

	block("run-openrc", c.sh("find /run/openrc | sort"))
	block("run-openrc-ls", c.sh("ls -lAR /run/openrc"))
	fileBlocks(t, "file", "/run/openrc")
	block("etc-runlevels", c.sh("ls -lAR /etc/runlevels"))
	// The pid files of a daemon of start-stop-daemon and of supervise-daemon (its other files are below /run/openrc/options).
	fileBlock(t, "file", "/run/t-daemon.pid")
	fileBlock(t, "file", "/run/supervise-t-super.pid")

	block("describe", c.step(c.service, "t-needs", "describe"))
	block("needsme", c.step(c.service, "t-daemon", "needsme"))
	block("ineed", c.step(c.service, "t-needs", "ineed"))
	block("iuse", c.step(c.service, "t-needs", "iuse"))
	block("iafter", c.step(c.service, "t-needs", "iafter"))
	block("start-fail", withStatus(failOut, failStatus))
	for _, s := range []string{"t-daemon", "t-super", "t-oneshot", "t-needs", "t-log", "t-crash", "t-fail"} {
		out, got := c.run(c.service, s, "status")
		block("status-"+s, withStatus(out, got))
	}
	block("ls-var-log", c.sh("ls -l /var/log"))
	c.finish()
}
