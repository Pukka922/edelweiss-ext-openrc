//go:build integration

package integration

import (
	"os/exec"
	"testing"
)

// TestRcStatusStates writes to the test log the rc-status output with services in the states starting, inactive and
// failed (rc-status-states, internal/openrc/testdata/rc-status-states.txt). OpenRC does not set "failed" by itself when a
// start fails, so the test makes each state with OpenRC's own means:
//   - starting: a service whose start takes three seconds, started in the background while rc-status runs;
//   - inactive: a service that calls mark_service_inactive in its start, as the net scripts do;
//   - failed: a service whose extra command breakit calls mark_service_failed.
func TestRcStatusStates(t *testing.T) {
	c := newCapture(t)
	writeService(t, "t-slow", "Test service whose start takes a while", "start() {\n\tebegin \"slow\"\n\tsleep 3\n\teend 0\n}\n")
	writeService(t, "t-inactive", "Test service that is inactive", "start() {\n\tebegin \"inactive\"\n\tmark_service_inactive\n\teend 0\n}\n")
	writeService(t, "t-markfailed", "Test service that marks itself failed", "extra_commands=\"breakit\"\nbreakit() {\n\tmark_service_failed\n}\n")
	c.run(c.update, "-u") // the dependency cache has to know the new scripts

	for _, step := range [][]string{{"t-inactive", "start"}, {"t-inactive", "status"}, {"t-markfailed", "breakit"}, {"t-markfailed", "status"}} {
		out, got := c.run(c.service, step[0], step[1])
		block(step[1]+"-"+step[0], withStatus(out, got))
	}
	block("ls-state-dirs", c.sh("ls -l /run/openrc/failed /run/openrc/inactive /run/openrc/starting"))

	cmd := exec.Command(c.service, "t-slow", "start")
	cmd.Env, cmd.Dir = exe.Env, "/"
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Wait()
	waitFor(t, "t-slow is starting", func() bool { return exists("/run/openrc/starting/t-slow") })
	block("rc-status-states", c.rcStatus())
	c.finish()
}
