package jobs_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/jobs"
	"github.com/Pukka922/edelweiss-ext-openrc/internal/run"
)

// control is the command of start, stop and restart: rc-service <service> <command>, with the time they get (a slow
// stop of docker).
func control(service, command string) *run.Cmd {
	return &run.Cmd{Path: "/sbin/rc-service", Args: []string{service, command}, Timeout: 5 * time.Minute}
}

// rcUpdate is rc-update <command> <service> <runlevel>.
func rcUpdate(command, service, runlevel string) *run.Cmd {
	return &run.Cmd{Path: "/sbin/rc-update", Args: []string{command, service, runlevel}, Timeout: 30 * time.Second}
}

// lbuCommit is the command that saves the changes of /etc in diskless and data mode.
func lbuCommit() *run.Cmd {
	return &run.Cmd{Path: "/usr/sbin/lbu", Args: []string{"commit"}, Timeout: 30 * time.Second}
}

// startStep, stopStep and restartStep are the steps of the same name: the command and the mark of the outcome.
func startStep(service string) jobs.Step {
	return jobs.Step{Op: "exec", Label: "start", Cmd: control(service, "start"), Service: service, Mark: jobs.MarkStart}
}

func stopStep(service string) jobs.Step {
	return jobs.Step{Op: "exec", Label: "stop", Cmd: control(service, "stop"), Service: service, Mark: jobs.MarkStop}
}

func restartStep(service string) jobs.Step {
	return jobs.Step{Op: "exec", Label: "restart", Cmd: control(service, "restart"), Service: service, Mark: jobs.MarkStart}
}

func TestDescribe(t *testing.T) {
	for _, c := range []struct {
		step jobs.Step
		want string
	}{
		{jobs.Step{Op: "exec", Label: "start", Cmd: control("sshd", "start")}, "exec /sbin/rc-service sshd start"},
		{jobs.Step{Op: "exec", Label: "enable", Cmd: rcUpdate("add", "sshd", "default")}, "exec /sbin/rc-update add sshd default"},
		{jobs.Step{Op: "exec", Label: "lbu", Cmd: lbuCommit()}, "exec /usr/sbin/lbu commit"},
		{jobs.Step{Op: "exec"}, "exec"},
		{jobs.Step{Op: "reboot"}, "reboot"},
		// the service and the mark are no part of the line: it is the command, as the log and the errors name the step
		{startStep("sshd"), "exec /sbin/rc-service sshd start"},
		{stopStep("net.eth0"), "exec /sbin/rc-service net.eth0 stop"},
		{restartStep("docker"), "exec /sbin/rc-service docker restart"},
		{jobs.Step{Op: "exec", Label: "restart", Cmd: control("docker", "restart"), Ignore: true}, "exec /sbin/rc-service docker restart"},
	} {
		if got := jobs.Describe(c.step); got != c.want {
			t.Errorf("Describe(%+v)\n got %q\nwant %q", c.step, got, c.want)
		}
	}
}

// A step is part of spec.json: the service and the mark go there, and only when there is one.
func TestStepJSON(t *testing.T) {
	data, err := json.Marshal(startStep("sshd"))
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"op":"exec","label":"start","cmd":{"path":"/sbin/rc-service","args":["sshd","start"],"timeout":300000000000},` +
		`"service":"sshd","mark":"start"}`
	if string(data) != want {
		t.Errorf("JSON\n got %s\nwant %s", data, want)
	}
	var got jobs.Step
	if err := json.Unmarshal(data, &got); err != nil || !reflect.DeepEqual(got, startStep("sshd")) {
		t.Errorf("round trip: %+v, %v", got, err)
	}
	// the marks of restart and stop are the strings the plan writes
	for _, c := range []struct {
		step jobs.Step
		mark string
	}{{restartStep("sshd"), `"mark":"start"`}, {stopStep("sshd"), `"mark":"stop"`}} {
		if data, err := json.Marshal(c.step); err != nil || !strings.HasSuffix(string(data), c.mark+"}") {
			t.Errorf("JSON of %s = %s, %v; want it to end with %s", c.step.Label, data, err, c.mark)
		}
	}
	plain, err := json.Marshal(jobs.Step{Op: "exec", Label: "lbu", Cmd: lbuCommit()})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"op":"exec","label":"lbu","cmd":{"path":"/usr/sbin/lbu","args":["commit"],"timeout":30000000000}}`; string(plain) != want {
		t.Errorf("JSON without a mark\n got %s\nwant %s", plain, want)
	}
}
