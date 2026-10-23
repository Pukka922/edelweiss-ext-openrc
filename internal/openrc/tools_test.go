package openrc

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/run"
)

func TestFindTools(t *testing.T) {
	f := newFixture(t)
	f.file("/usr/sbin/rc-service", "", 0o644) // not executable: the next directory counts
	f.file("/sbin/rc-service", "", 0o755)
	f.file("/usr/bin/rc-update/keep", "", 0o644) // a directory of that name
	f.file("/sbin/rc-update", "", 0o755)
	f.file("/bin/rc-update", "", 0o755) // a later directory: not taken
	f.file("/bin/rc-status", "", 0o755)
	f.file("/usr/libexec/lbu", "", 0o755)
	f.link("/usr/sbin/lbu", "../libexec/lbu") // a link to an executable file counts
	want := Tools{RcService: "/sbin/rc-service", RcUpdate: "/sbin/rc-update", RcStatus: "/bin/rc-status", Lbu: "/usr/sbin/lbu"}
	if got := FindTools(f.root); got != want {
		t.Errorf("FindTools:\n got %+v\nwant %+v", got, want)
	}
	if got := FindTools(t.TempDir()); got != (Tools{}) {
		t.Errorf("FindTools on an empty root = %+v, want nothing", got)
	}
}

func TestEnv(t *testing.T) {
	want := []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C", "EINFO_COLOR=NO"}
	if !reflect.DeepEqual(Env, want) || !reflect.DeepEqual(Env, run.Env) {
		t.Errorf("Env = %q, want %q (run.Env %q)", Env, want, run.Env)
	}
}

func TestToolsCommands(t *testing.T) {
	tools := Tools{RcService: "/sbin/rc-service", RcUpdate: "/sbin/rc-update", RcStatus: "/bin/rc-status", Lbu: "/usr/sbin/lbu"}
	const long, short = 5 * time.Minute, 30 * time.Second
	for _, tc := range []struct {
		cmd     run.Cmd
		line    string
		timeout time.Duration
	}{
		{tools.Start("sshd"), "/sbin/rc-service sshd start", long},
		{tools.Stop("net.eth0"), "/sbin/rc-service net.eth0 stop", long},
		{tools.Restart("docker"), "/sbin/rc-service docker restart", long},
		{tools.Enable("sshd", "default"), "/sbin/rc-update add sshd default", short},
		{tools.Enable("php-fpm83", "boot"), "/sbin/rc-update add php-fpm83 boot", short},
		{tools.Disable("sshd", "boot"), "/sbin/rc-update del sshd boot", short},
		{tools.LbuCommit(), "/usr/sbin/lbu commit", short},
		{tools.status(), "/bin/rc-status --all --nocolor", short},
		{tools.query("t-needs", "ineed"), "/sbin/rc-service t-needs ineed", short},
		{tools.query("t-daemon", "needsme"), "/sbin/rc-service t-daemon needsme", short},
	} {
		c := tc.cmd
		if got := c.String(); got != tc.line {
			t.Errorf("command line\n got %q\nwant %q", got, tc.line)
			continue
		}
		// Every word is an argument of its own: nothing that a shell would have split is one argument.
		if words := strings.Fields(tc.line); len(c.Args) != len(words)-1 {
			t.Errorf("%s: %d arguments %q, want %d", tc.line, len(c.Args), c.Args, len(words)-1)
		}
		if c.Timeout != tc.timeout || c.OK != nil || c.Codes != nil {
			t.Errorf("%s: Timeout %v OK %v Codes %v, want Timeout %v and nothing else", tc.line, c.Timeout, c.OK, c.Codes, tc.timeout)
		}
	}
}
