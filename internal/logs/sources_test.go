package logs

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/openrc"
)

// system is a fake system below a temp root with the files of /etc/init.d, /etc/conf.d and /var/log that the logs are
// found in. Paths are those of the system ("/var/log/docker.log"); the directories are made as needed.
type system struct {
	t    *testing.T
	root string
}

func newSystem(t *testing.T) *system {
	sys := &system{t: t, root: t.TempDir()}
	sys.dir(logDir)
	return sys
}

// path is p below the root.
func (sys *system) path(p string) string { return filepath.Join(sys.root, p) }

func (sys *system) dir(p string) {
	sys.t.Helper()
	if err := os.MkdirAll(sys.path(p), 0o755); err != nil {
		sys.t.Fatal(err)
	}
}

func (sys *system) file(p, content string) {
	sys.t.Helper()
	sys.dir(filepath.Dir(p))
	if err := os.WriteFile(sys.path(p), []byte(content), 0o644); err != nil {
		sys.t.Fatal(err)
	}
}

// add appends text to the file p.
func (sys *system) add(p, text string) {
	sys.t.Helper()
	f, err := os.OpenFile(sys.path(p), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		sys.t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		sys.t.Fatal(err)
	}
}

// link makes p a symbolic link to target, taken as it is: an absolute target is a path of the host.
func (sys *system) link(p, target string) {
	sys.t.Helper()
	sys.dir(filepath.Dir(p))
	if err := os.Symlink(target, sys.path(p)); err != nil {
		sys.t.Fatal(err)
	}
}

// lock makes p unreadable (mode 0000) until the test ends.
func (sys *system) lock(p string) {
	sys.t.Helper()
	if err := os.Chmod(sys.path(p), 0); err != nil {
		sys.t.Fatal(err)
	}
	sys.t.Cleanup(func() { _ = os.Chmod(sys.path(p), 0o755) }) // before the temp dir is removed
}

// age sets the modification time of p to d before now.
func (sys *system) age(p string, d time.Duration) {
	sys.t.Helper()
	at := time.Now().Add(-d)
	if err := os.Chtimes(sys.path(p), at, at); err != nil {
		sys.t.Fatal(err)
	}
}

// service writes the init script of name with the lines script and, unless conf is "", /etc/conf.d/<name>. It is the
// service as openrc.Reader lists it.
func (sys *system) service(name, script, conf string) openrc.Service {
	sys.t.Helper()
	sys.file("/etc/init.d/"+name, "#!/sbin/openrc-run\n"+script)
	if conf != "" {
		sys.file("/etc/conf.d/"+name, conf)
	}
	return openrc.Service{Name: name, Script: "/etc/init.d/" + name}
}

// readable is a source the process can read, labelled with its path.
func readable(id, p string) Source { return Source{ID: id, Label: p, Path: p, Readable: true} }

// syslogSource is the source of the service's lines of the system log.
func syslogSource(service string) Source {
	return Source{ID: "syslog", Label: "/var/log/messages (nur " + service + ")", Path: "/var/log/messages", Readable: true}
}

func TestSourcesOutputLog(t *testing.T) {
	// As in Alpine's docker service: the variables of the defaults are what /etc/conf.d/docker may set.
	docker := `output_log="${DOCKER_LOGFILE:-/var/log/${RC_SVCNAME}.log}"` + "\n" +
		`error_log="${DOCKER_ERRFILE:-/var/log/docker.err}"` + "\n"
	for _, tc := range []struct {
		name, script, conf string
		files              []string // made empty
		setup              func(sys *system)
		want               []Source
	}{
		{name: "conf.d sets the variable of a default", script: docker,
			conf:  "# where the output of dockerd goes\nDOCKER_LOGFILE=\"/var/log/docker/daemon.txt\"\n#DOCKER_ERRFILE=/var/log/other.err\n",
			files: []string{"/var/log/docker/daemon.txt", "/var/log/docker.err", "/var/log/other.err"},
			want:  []Source{readable("output", "/var/log/docker/daemon.txt"), readable("error", "/var/log/docker.err")}},
		{name: "the defaults, with the service's name", script: docker,
			files: []string{"/var/log/docker.log", "/var/log/docker.err"},
			// /var/log/docker.log is also /var/log/<s>.log: one source
			want: []Source{readable("output", "/var/log/docker.log"), readable("error", "/var/log/docker.err")}},
		{name: "the forms of the service's name",
			script: "output_log=/var/log/$RC_SVCNAME/${SVCNAME}.out\n\terror_log=\"/var/log/${RC_SVCNAME}-$SVCNAME.err\" # both\n",
			files:  []string{"/var/log/docker/docker.out", "/var/log/docker-docker.err"},
			want:   []Source{readable("output", "/var/log/docker/docker.out"), readable("error", "/var/log/docker-docker.err")}},
		{name: "both forms of a default", script: "output_log=${OUT-/var/log/a.txt}\nerror_log=\"${ERR:-/var/log/b.txt}\"\n",
			files: []string{"/var/log/a.txt", "/var/log/b.txt"},
			want:  []Source{readable("output", "/var/log/a.txt"), readable("error", "/var/log/b.txt")}},
		{name: "empty values in conf.d", script: "output_log=${OUT-/var/log/a.txt}\nerror_log=\"${ERR:-/var/log/b.txt}\"\n",
			conf: "OUT=\nERR=''\n", files: []string{"/var/log/a.txt", "/var/log/b.txt"},
			// ${OUT-…} keeps the empty value (no path), ${ERR:-…} takes the default
			want: []Source{readable("error", "/var/log/b.txt")}},
		{name: "the script assigns the variable of a default",
			script: "ERRFILE=/var/log/mine.err\noutput_log=\"${LOGFILE:-/var/log/default.txt}\"\n" +
				"error_log=\"${ERRFILE:-/var/log/default.err}\"\nstart_pre() {\n\tLOGFILE=/var/log/other.txt\n}\n",
			conf:  "LOGFILE=/var/log/conf.txt\n",
			files: []string{"/var/log/mine.err", "/var/log/default.txt", "/var/log/default.err", "/var/log/other.txt", "/var/log/conf.txt"},
			// before the line or in a function: what the variable holds at the line is not known, the default is no
			// more than a guess
			want: []Source{}},
		{name: "conf.d assigns the variable of a default after the line",
			conf: "output_log=\"${LOGFILE:-/var/log/default.txt}\"\nLOGFILE=/var/log/later.txt\nERRFILE=/var/log/conf.err\n" +
				"error_log=\"${ERRFILE:-/var/log/default.err}\"\n",
			files: []string{"/var/log/default.txt", "/var/log/later.txt", "/var/log/conf.err", "/var/log/default.err"},
			want:  []Source{readable("error", "/var/log/conf.err")}},
		{name: "the script assigns the variable of a default in conf.d",
			script: "start_pre() {\n\tERRFILE=/var/log/mine.err\n}\n",
			conf:   "ERRFILE=/var/log/conf.err\nerror_log=\"${ERRFILE:-/var/log/default.err}\"\n",
			files:  []string{"/var/log/mine.err", "/var/log/conf.err", "/var/log/default.err"}, want: []Source{}},
		{name: "the script's default of conf.d's own value",
			script: "output_log=\"${output_log:-/var/log/default.txt}\"\nerror_log=\"${error_log:-/var/log/default.err}\"\n",
			conf:   "output_log=/var/log/conf.txt\n",
			files:  []string{"/var/log/default.txt", "/var/log/conf.txt", "/var/log/default.err"},
			want:   []Source{readable("output", "/var/log/conf.txt"), readable("error", "/var/log/default.err")}},
		{name: "the script wins over conf.d, the last assignment wins",
			script: "output_log=/var/log/first.txt\noutput_log='/var/log/script.txt'\n",
			conf:   "output_log=/var/log/conf.txt\nerror_log=/var/log/conf.err\n",
			files:  []string{"/var/log/first.txt", "/var/log/script.txt", "/var/log/conf.txt", "/var/log/conf.err"},
			want:   []Source{readable("output", "/var/log/script.txt"), readable("error", "/var/log/conf.err")}},
		{name: "a command substitution", script: "output_log=\"$(echo /var/log/a.txt)\"\nerror_log=`echo /var/log/a.txt`\n",
			files: []string{"/var/log/a.txt"}, want: []Source{}},
		{name: "backticks amid the path", script: "output_log=/var/log/`x`.txt\nerror_log=\"/var/log/`x`.txt\"\n",
			files: []string{"/var/log/`x`.txt"}, want: []Source{}}, // a file of that name is no reason to take them literally
		{name: "other variables", script: "output_log=$LOGDIR/a.txt\nerror_log=\"${LOGDIR}/a.txt\"\n", conf: "LOGDIR=/var/log\n",
			files: []string{"/var/log/a.txt"}, want: []Source{}},
		{name: "a value in conf.d that is not literal, another form of ${…}",
			script: "output_log=\"${OUT:-/var/log/a.txt}\"\nerror_log=${ERR:=/var/log/a.txt}\n", conf: "OUT=\"$(cat /etc/out)\"\n",
			files: []string{"/var/log/a.txt"}, want: []Source{}},
		{name: "an assignment for one command", script: "output_log=/var/log/a.txt run\n",
			files: []string{"/var/log/a.txt"}, want: []Source{}},
		{name: "a path outside /var/log", script: "output_log=/tmp/a.txt\n", files: []string{"/tmp/a.txt"}, want: []Source{}},
		{name: "relative, not clean", script: "output_log=var/log/a.txt\nerror_log=/var/log//a.txt\n",
			files: []string{"/var/log/a.txt"}, want: []Source{}},
		{name: "a file that is not there", script: "output_log=/var/log/none.txt\n", want: []Source{}},
		{name: "links that leave /var/log", script: "output_log=/var/log/rel.txt\nerror_log=/var/log/abs.txt\n",
			files: []string{"/etc/secret.txt"},
			setup: func(sys *system) {
				sys.link("/var/log/rel.txt", "../../etc/secret.txt")
				sys.link("/var/log/abs.txt", sys.path("/etc/secret.txt"))
			},
			want: []Source{}},
		{name: "links that end below /var/log", script: "output_log=/var/log/current.txt\nerror_log=/srv/err.txt\n",
			files: []string{"/var/log/docker/2026.txt", "/var/log/srv.err"},
			setup: func(sys *system) {
				sys.link("/var/log/current.txt", "docker/2026.txt")
				sys.link("/srv/err.txt", sys.path("/var/log/srv.err"))
			},
			// the file read is the one the links end at
			want: []Source{readable("output", "/var/log/docker/2026.txt"), readable("error", "/var/log/srv.err")}},
		{name: "one file for both", script: "output_log=/var/log/docker.log\nerror_log=/var/log/docker.log\n",
			files: []string{"/var/log/docker.log"}, want: []Source{readable("output", "/var/log/docker.log")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sys := newSystem(t)
			s := sys.service("docker", tc.script, tc.conf)
			for _, p := range tc.files {
				sys.file(p, "")
			}
			if tc.setup != nil {
				tc.setup(sys)
			}
			if got := Sources(sys.root, s, nil); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Sources\n got %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestSourcesFiles(t *testing.T) {
	sys := newSystem(t)
	s := sys.service("nginx", "command=/usr/sbin/nginx\n", "")
	sys.file("/var/log/nginx.log", "")
	sys.file("/etc/secret.log", "")
	for name, age := range map[string]time.Duration{"access.log": time.Hour, "error.log": 2 * time.Hour,
		"old.log": 72 * time.Hour, "older.log": 96 * time.Hour, "notes.txt": 0, ".hidden.log": 0} {
		sys.file("/var/log/nginx/"+name, "")
		sys.age("/var/log/nginx/"+name, age)
	}
	sys.dir("/var/log/nginx/dir.log")                                // newest, but no file
	sys.link("/var/log/nginx/escape.log", "../../../etc/secret.log") // newest, but outside /var/log
	sys.file("/var/log/messages", "")
	want := []Source{readable("file:nginx.log", "/var/log/nginx.log"),
		readable("file:nginx/access.log", "/var/log/nginx/access.log"),
		readable("file:nginx/error.log", "/var/log/nginx/error.log"),
		readable("file:nginx/old.log", "/var/log/nginx/old.log"),
		syslogSource("nginx")}
	if got := Sources(sys.root, s, []string{"nginx"}); !reflect.DeepEqual(got, want) {
		t.Errorf("Sources\n got %+v\nwant %+v", got, want)
	}
	// The system log needs a program whose lines it can tell.
	for _, programs := range [][]string{nil, {"sh", "start-stop-daemon"}} {
		if got := Sources(sys.root, s, programs); !reflect.DeepEqual(got, want[:4]) {
			t.Errorf("Sources with the programs %q\n got %+v\nwant %+v", programs, got, want[:4])
		}
	}
	if got := Sources(sys.root, openrc.Service{Name: "../nginx"}, []string{"nginx"}); !reflect.DeepEqual(got, []Source{}) {
		t.Errorf("Sources of an invalid name = %+v, want none", got)
	}
	none := newSystem(t)
	if got := Sources(none.root, none.service("nginx", "", ""), []string{"nginx"}); !reflect.DeepEqual(got, []Source{}) {
		t.Errorf("Sources without any log = %+v, want none", got)
	}
}

func TestNotReadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a file of mode 0000")
	}
	sys := newSystem(t)
	// As the capture found them: output_log and error_log of root's services are mode 0640.
	s := sys.service("t-log", "output_log=/var/log/current.log\nerror_log=/var/log/private/t-log.err\n", "")
	sys.file("/var/log/t-log.log", "tick\n")
	sys.link("/var/log/current.log", "t-log.log")
	sys.file("/var/log/private/t-log.err", "")
	sys.file("/var/log/messages", "")
	sys.lock("/var/log/t-log.log")
	sys.lock("/var/log/private") // its links cannot be resolved: the source keeps the configured path
	sys.lock("/var/log/messages")
	want := []Source{{ID: "output", Label: "/var/log/t-log.log", Path: "/var/log/t-log.log"},
		{ID: "error", Label: "/var/log/private/t-log.err", Path: "/var/log/private/t-log.err"},
		{ID: "syslog", Label: "/var/log/messages (nur t-log)", Path: "/var/log/messages"}}
	got := Sources(sys.root, s, []string{"t-log"})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Sources\n got %+v\nwant %+v", got, want)
	}
	for _, src := range want {
		if lines, err := Tail(sys.root, src, []string{"t-log"}, 500); err == nil {
			t.Errorf("Tail of %s = %+v, want an error", src.Path, lines)
		}
	}

	// A path outside /var/log that cannot be resolved is no source.
	s = sys.service("web", "output_log=/etc/private/web.log\n", "")
	sys.file("/etc/private/web.log", "")
	sys.lock("/etc/private")
	if got := Sources(sys.root, s, nil); !reflect.DeepEqual(got, []Source{}) {
		t.Errorf("Sources of web = %+v, want none", got)
	}
}
