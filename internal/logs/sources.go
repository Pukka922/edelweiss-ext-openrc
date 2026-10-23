// Package logs finds the logs of a service and reads them: the last lines (Tail) and the lines added since (Follow).
// The logs are the service's output_log and error_log, /var/log/<s>.log, the newest files of /var/log/<s>/ and its
// lines of the system log, /var/log/messages of busybox syslogd, which also tells the services whose start failed since
// the boot (BootFailures). Only files below /var/log, after their links are resolved, are ever read. The formats are
// those of the real OpenRC and busybox of Alpine, captured in internal/openrc/testdata.
package logs

import (
	"cmp"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/openrc"
)

// logDir is the directory every log is in, once its links are resolved.
const logDir = "/var/log"

// The source of the system log: /var/log/messages of busybox syslogd.
const (
	syslogID   = "syslog"
	syslogPath = logDir + "/messages"
)

// newest is how many files of /var/log/<s>/ are sources: those changed last.
const newest = 3

// Source is a log of a service.
type Source struct {
	ID       string `json:"id"`    // "output", "error", "file:<path below /var/log>", "syslog"
	Label    string `json:"label"` // shown in the select: the path, "/var/log/messages (nur <s>)"
	Path     string `json:"path"`
	Readable bool   `json:"readable"`
}

// Sources are the logs of the service s, in this order: its output_log and error_log (configured), /var/log/<s>.log
// ("file:<s>.log"), the three newest *.log files of /var/log/<s>/ ("file:<s>/<name>") and, when programs (see
// matcher) holds a name that is not generic, its lines of /var/log/messages. Each is a regular file below /var/log once
// its links are resolved, and Path is that file; a file that is not there or whose links lead out of /var/log is left
// out, and a file comes once (as the first source it is). A file the process may not read is there with Readable
// false, by its configured path when the process may not even resolve it. An invalid name has none.
func Sources(root string, s openrc.Service, programs []string) []Source {
	sources := []Source{}
	if !openrc.ValidName(s.Name) {
		return sources
	}
	conf := assignments(readText(filepath.Join(root, "/etc/conf.d", s.Name)))
	script := assignments(readText(filepath.Join(root, "/etc/init.d", s.Name)))
	for _, v := range []struct{ id, name string }{{"output", "output_log"}, {"error", "error_log"}} {
		if p, ok := configured(s.Name, v.name, script, conf); ok {
			sources = add(sources, root, v.id, p)
		}
	}
	sources = add(sources, root, "file:"+s.Name+".log", logDir+"/"+s.Name+".log")
	listed := len(sources)
	for _, name := range logFiles(root, s.Name) {
		if len(sources)-listed == newest {
			break
		}
		sources = add(sources, root, "file:"+s.Name+"/"+name, logDir+"/"+s.Name+"/"+name)
	}
	if len(matcher(programs)) > 0 { // the lines of the service, not the file: it may be a source of its own already
		if p, readable, ok := probe(root, syslogPath); ok {
			sources = append(sources, Source{ID: syslogID, Label: syslogPath + " (nur " + s.Name + ")", Path: p,
				Readable: readable})
		}
	}
	return sources
}

// add appends the source id of the log file p (a path of the system below root) as probe finds it, unless there is
// none or its file is a source already.
func add(sources []Source, root, id, p string) []Source {
	file, readable, ok := probe(root, p)
	if !ok || slices.ContainsFunc(sources, func(s Source) bool { return s.Path == file }) {
		return sources
	}
	return append(sources, Source{ID: id, Label: file, Path: file, Readable: readable})
}

// probe finds the log file p: file is where it is once its links are resolved, readable whether the process can open
// it. ok is false when it is not there, is no regular file or its links lead out of /var/log. A path below /var/log
// that the process may not resolve (a directory on the way it may not search) is kept as it is, not readable.
func probe(root, p string) (file string, readable, ok bool) {
	f, file, err := open(root, p)
	switch {
	case err == nil:
		f.Close()
		return file, true, true
	case !errors.Is(err, fs.ErrPermission):
		return "", false, false
	case file != "":
		return file, false, true
	}
	return p, false, strings.HasPrefix(p, logDir+"/")
}

// Errors of open.
var (
	errOutside = errors.New("not a log file below " + logDir)
	errNotFile = errors.New("not a regular file")
)

// open opens the log file p, an absolute path of the system below root, for reading: the regular file it names once
// its links are resolved, which has to be below /var/log. file is where that is ("/var/log/…"), also with an error once
// it is known. The open goes through an os.Root of /var/log, so a link changed meanwhile cannot lead it out, and it does
// not wait for the writer of a FIFO.
func open(root, p string) (f *os.File, file string, err error) {
	if !path.IsAbs(p) {
		return nil, "", errOutside
	}
	dir, err := filepath.EvalSymlinks(filepath.Join(root, logDir))
	if err != nil {
		return nil, "", err
	}
	target, err := filepath.EvalSymlinks(filepath.Join(root, p))
	if err != nil {
		return nil, "", err
	}
	rel, err := filepath.Rel(dir, target)
	if err != nil || !filepath.IsLocal(rel) { // os.Root would refuse it as well; this keeps file below /var/log
		return nil, "", errOutside
	}
	file = path.Join(logDir, filepath.ToSlash(rel))
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, file, err
	}
	defer r.Close()
	if f, err = r.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0); err != nil {
		return nil, file, err
	}
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, file, cmp.Or(err, errNotFile)
	}
	return f, file, nil
}

// logFiles are the names of the *.log entries of /var/log/<name>/ (not the hidden ones), the newest first: by the time
// they were changed, then by name. Whether one is a log file is for probe to tell.
func logFiles(root, name string) []string {
	dir := filepath.Join(root, logDir, name)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type file struct {
		name    string
		changed time.Time
	}
	var files []file
	for _, e := range entries { // sorted by name
		if strings.HasPrefix(e.Name(), ".") || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		if st, err := os.Stat(filepath.Join(dir, e.Name())); err == nil {
			files = append(files, file{e.Name(), st.ModTime()})
		}
	}
	slices.SortStableFunc(files, func(a, b file) int { return b.changed.Compare(a.changed) })
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = f.name
	}
	return names
}

// readText is the content of the regular file, "" when it cannot be read.
func readText(file string) string {
	if st, err := os.Stat(file); err != nil || !st.Mode().IsRegular() {
		return ""
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	return string(b)
}

// assignment is a line that assigns a shell variable: blanks, the name, "=", the value as written.
var assignment = regexp.MustCompile(`^[ \t]*([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)

// assign is a line that assigns a shell variable: the name and the value as written after the "=".
type assign struct{ name, value string }

// assignments are the lines of the text that assign shell variables, in their order.
func assignments(text string) []assign {
	var lines []assign
	for _, line := range strings.Split(text, "\n") {
		if m := assignment.FindStringSubmatch(line); m != nil {
			lines = append(lines, assign{m[1], m[2]})
		}
	}
	return lines
}

// lastOf is the index of the last of the lines that assigns name, -1 when none does.
func lastOf(lines []assign, name string) int {
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i].name == name {
			return i
		}
	}
	return -1
}

// configured is the path the variable (output_log, error_log) of the service is set to by its last line: in the
// script, else in conf.d (OpenRC reads conf.d first, then the script), expanded as the shell does it (see shell) with
// what the lines around it tell (see scope). ok is false when neither file sets it, its value is not known without
// running the shell, or it is not a clean path (open takes absolute paths only).
func configured(service, variable string, script, conf []assign) (p string, ok bool) {
	var v string
	sc := &scope{}
	if i := lastOf(script, variable); i >= 0 {
		v, sc.before, sc.other = script[i].value, conf, slices.Delete(slices.Clone(script), i, i+1)
	} else if i := lastOf(conf, variable); i >= 0 {
		v, sc.before, sc.other = conf[i].value, conf[:i], append(slices.Clone(conf[i+1:]), script...)
	} else {
		return "", false
	}
	p, ok = shell{service: service, scope: sc}.value(v)
	return p, ok && path.Clean(p) == p
}

// shell expands the value of an assignment line as far as that is known without running the shell: quotes,
// backslashes, the service's name ($RC_SVCNAME, ${RC_SVCNAME}, $SVCNAME, ${SVCNAME}) and ${VAR:-default}/${VAR-default}
// (see fallback). Anything else (a command substitution, a backtick, another variable, another form of ${…}, an
// operator) makes the value unknown.
type shell struct {
	service string // the value of RC_SVCNAME and SVCNAME
	scope   *scope // the lines around the line expanded; nil for a value of conf.d, which has to be literal
}

// scope are the other assignment lines of conf.d and the script, seen from the line being expanded.
type scope struct {
	before []assign // the lines of conf.d before it (OpenRC reads conf.d first): they set what it sees
	other  []assign // the lines that may set a variable without telling when: the script's others, conf.d's after it
}

// value is the value of the assignment written as v (the text after "="), and whether it is known. Only blanks, a ";"
// or a comment may follow the word: "output_log=x cmd" assigns for cmd alone.
func (sh shell) value(v string) (string, bool) {
	w := &word{s: v, sh: sh}
	out, ok := w.read(false)
	rest := strings.TrimLeft(w.s[w.i:], " \t")
	return out, ok && (rest == "" || rest[0] == ';' || rest[0] == '#')
}

// variable is the value of $name or ${name}: only the service's own name is known.
func (sh shell) variable(name string) (string, bool) {
	if name == "RC_SVCNAME" || name == "SVCNAME" {
		return sh.service, true
	}
	return "", false
}

// fallback is the value of ${name:-def} (colon) or ${name-def}: the service's name for its variables; unknown when a
// line of other assigns name (the script, before the line or in a function; conf.d after it); else the value of the
// last line before it that assigns name, which has to be literal (only the service's name in it); def only when no
// line assigns name at all (with the colon also when the value is ""). Never a guessed path.
func (sh shell) fallback(name, def string, colon bool) (string, bool) {
	if v, ok := sh.variable(name); ok {
		return v, true
	}
	if sh.scope == nil || lastOf(sh.scope.other, name) >= 0 {
		return "", false
	}
	i := lastOf(sh.scope.before, name)
	if i < 0 {
		return def, true
	}
	v, ok := shell{service: sh.service}.value(sh.scope.before[i].value)
	if colon && ok && v == "" {
		return def, true
	}
	return v, ok
}

// word reads a shell word from s, from the byte i on.
type word struct {
	s  string
	i  int
	sh shell
}

// varName is the name of a shell variable at the start of a text.
var varName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*`)

// read reads the word up to an unquoted blank or ";" or, inBrace, up to the unquoted "}" that closes the ${…} whose
// default it is (blanks belong to it then). ok is false for what shell does not know.
func (w *word) read(inBrace bool) (string, bool) {
	var b strings.Builder
	for w.i < len(w.s) {
		c := w.s[w.i]
		switch {
		case inBrace && c == '}':
			return b.String(), true
		case !inBrace && (c == ' ' || c == '\t' || c == ';'):
			return b.String(), true
		case c == '\'':
			end := strings.IndexByte(w.s[w.i+1:], '\'')
			if end < 0 {
				return "", false
			}
			b.WriteString(w.s[w.i+1 : w.i+1+end])
			w.i += end + 2
		case c == '"':
			w.i++
			if !w.quoted(&b) {
				return "", false
			}
		case c == '\\' && w.i+1 < len(w.s):
			b.WriteByte(w.s[w.i+1])
			w.i += 2
		case c == '$':
			v, ok := w.expansion()
			if !ok {
				return "", false
			}
			b.WriteString(v)
		case strings.IndexByte("\\`|&<>()", c) >= 0:
			return "", false
		default:
			b.WriteByte(c)
			w.i++
		}
	}
	return b.String(), !inBrace
}

// quoted reads the rest of a double-quoted string, after its opening quote, up to its closing quote: a backslash keeps
// only $, `, " and \ from their meaning.
func (w *word) quoted(b *strings.Builder) bool {
	for w.i < len(w.s) {
		c := w.s[w.i]
		switch {
		case c == '"':
			w.i++
			return true
		case c == '\\' && w.i+1 < len(w.s) && strings.IndexByte("$`\"\\", w.s[w.i+1]) >= 0:
			b.WriteByte(w.s[w.i+1])
			w.i += 2
		case c == '$':
			v, ok := w.expansion()
			if !ok {
				return false
			}
			b.WriteString(v)
		case c == '`':
			return false
		default:
			b.WriteByte(c)
			w.i++
		}
	}
	return false
}

// expansion reads the expansion at the "$" at i: $NAME, ${NAME}, ${NAME:-word} or ${NAME-word}.
func (w *word) expansion() (string, bool) {
	w.i++
	braced := strings.HasPrefix(w.s[w.i:], "{")
	if braced {
		w.i++
	}
	name := varName.FindString(w.s[w.i:])
	if name == "" {
		return "", false
	}
	w.i += len(name)
	rest := w.s[w.i:]
	switch {
	case !braced:
		return w.sh.variable(name)
	case strings.HasPrefix(rest, "}"):
		w.i++
		return w.sh.variable(name)
	case strings.HasPrefix(rest, ":-"):
		w.i += 2
		return w.defaulted(name, true)
	case strings.HasPrefix(rest, "-"):
		w.i++
		return w.defaulted(name, false)
	}
	return "", false
}

// defaulted reads the default of ${name:-…} (colon) or ${name-…} and its closing brace, and expands it.
func (w *word) defaulted(name string, colon bool) (string, bool) {
	def, ok := w.read(true)
	if !ok {
		return "", false
	}
	w.i++ // the closing brace
	return w.sh.fallback(name, def, colon)
}
