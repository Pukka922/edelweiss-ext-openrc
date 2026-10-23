//go:build integration

package integration

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/actions"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/jobs"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/openrc"
)

// hangLimit is the time limit of the start of t-hang in the test, in place of the 5 minutes of a plan.
const hangLimit = 2 * time.Second

// hanging lists the processes of t-hang's hang that are there: its sleep, which ignores SIGTERM ("sleep 86400"). A
// zombie that --init has not reaped yet is gone.
func hanging(t *testing.T) []string {
	t.Helper()
	dirs, err := filepath.Glob("/proc/[0-9]*")
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, dir := range dirs {
		cmdline, err := os.ReadFile(dir + "/cmdline")
		stat, statErr := os.ReadFile(dir + "/stat")
		if err != nil || statErr != nil || !bytes.Equal(cmdline, []byte("sleep\x0086400\x00")) {
			continue
		}
		if _, after, _ := strings.Cut(string(stat), ") "); !strings.HasPrefix(after, "Z") {
			found = append(found, filepath.Base(dir))
		}
	}
	return found
}

// TestTimeoutAgainstOpenRC starts t-hang, whose start never ends, through the plan and the executor of a job, with a
// time limit of 2 s. When the limit is over, run.Exec sends SIGTERM to the process group of rc-service, which is
// openrc-run by then: it rolls the start back on its way out, so that OpenRC has t-hang stopped again, not starting,
// and frees its lock. What is left of the group (the sleep of t-hang ignores SIGTERM) is killed. The step fails, and
// the failed marker makes the page show the failed start, with the button to start it again.
func TestTimeoutAgainstOpenRC(t *testing.T) {
	a := newActor(t)
	snap, err := a.r.Read(ctx)
	if err != nil {
		t.Fatalf("reading the services: %v", err)
	}
	plan, err := actions.Build(ctx, snap, a.r.Dependents, actions.Request{Kind: "start", Service: "t-hang"}, a.env)
	if err != nil || len(plan.Steps) != 1 || plan.Steps[0].Cmd == nil {
		t.Fatalf("the plan of the start of t-hang: %+v, %v", plan, err)
	}
	step := plan.Steps[0]
	limited := *step.Cmd
	limited.Timeout = hangLimit
	step.Cmd = &limited

	start := time.Now()
	err = a.x.Do(ctx, step)
	took := time.Since(start)
	var se *jobs.StepError
	if !errors.As(err, &se) || se.Code != actions.CodeFailed || !strings.Contains(se.Message, "deadline exceeded") {
		t.Errorf("the start of t-hang: %v, want a failed step that names the time limit", err)
	} else {
		t.Logf("the start of t-hang after %v: %s\n%s", took, se.Message, se.Output)
	}
	if took > hangLimit+20*time.Second {
		t.Errorf("the start of t-hang ended after %v: openrc-run did not end on SIGTERM, it was killed", took)
	}

	a.state(openrc.Stopped, "t-hang")
	if exists("/run/openrc/starting/t-hang") || exists("/run/openrc/started/t-hang") {
		t.Error("OpenRC still has t-hang starting or started")
	}
	if failed, _ := marks(t); failed["t-hang"].IsZero() {
		t.Errorf("no failed marker for t-hang after its start ran out of time: %v", failed)
	}
	waitFor(t, "the hang of t-hang is gone", func() bool { return len(hanging(t)) == 0 })

	// OpenRC freed the lock of t-hang: its status answers at once, stopped (exit 3)
	out, got := newCapture(t).run(a.r.Tools.RcService, "t-hang", "status")
	if got != 3 {
		t.Errorf("rc-service t-hang status: exit %d, want 3 (stopped)\n%s", got, out)
	}
}
