package openrc

import (
	"bufio"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// The directories of the services and their autostart, below the root.
const (
	initDir     = "/etc/init.d"
	runlevelDir = "/etc/runlevels"
)

// maxLinks is how many symbolic links scriptFile follows from an entry of /etc/init.d to its script.
const maxLinks = 8

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)

// ValidName reports whether s can be the name of a service: a letter or digit, then up to 63 letters, digits and
// ".", "_", "+", "-". It never holds a "/", a space or a leading dot.
func ValidName(s string) bool { return validName.MatchString(s) }

// scan lists the services of /etc/init.d, sorted by name: each entry with a valid name that is an executable regular
// file, or a symbolic link that ends at one in /etc/init.d (net.eth0 → net.lo keeps its own name). Each has its
// description and script path; the other fields are left for Read.
func (r Reader) scan() ([]Service, error) {
	entries, err := os.ReadDir(filepath.Join(r.Root, initDir))
	if err != nil {
		return nil, err
	}
	services := []Service{}
	for _, e := range entries { // sorted by name
		if !ValidName(e.Name()) {
			continue
		}
		if file, ok := r.scriptFile(e.Name()); ok {
			services = append(services, Service{Name: e.Name(), Description: description(file),
				Script: initDir + "/" + e.Name()})
		}
	}
	return services, nil
}

// scriptFile is the script of the entry name of /etc/init.d, as a path on the host, and whether there is one. Links are
// followed below the root: an absolute target is a path of the system the root holds, a relative one is relative to
// the link's directory. The script must be an executable regular file directly in /etc/init.d.
func (r Reader) scriptFile(name string) (string, bool) {
	p := initDir + "/" + name // the path in the system below the root
	for range maxLinks {
		file := filepath.Join(r.Root, p)
		st, err := os.Lstat(file)
		switch {
		case err != nil:
			return "", false
		case st.Mode().IsRegular():
			return file, path.Dir(p) == initDir && st.Mode()&0o111 != 0
		case st.Mode()&fs.ModeSymlink == 0:
			return "", false
		}
		target, err := os.Readlink(file)
		if err != nil {
			return "", false
		}
		if path.IsAbs(target) {
			p = path.Clean(target)
		} else {
			p = path.Join(path.Dir(p), target)
		}
	}
	return "", false
}

// description is the value of the first description= line of the script, as the script writes it: the text between
// single quotes, the text between double quotes (a backslash only keeps a following ", \, $ or ` from its meaning), or
// a bare word; nothing is expanded. A quote that is not closed takes the rest of the line. It is "" when the script
// has none or cannot be read.
func description(file string) string {
	f, err := os.Open(file)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(strings.TrimLeft(sc.Text(), " \t"), "description="); ok {
			return shellValue(v)
		}
	}
	return ""
}

// shellValue is the value of a shell assignment whose text after the "=" is v (see description).
func shellValue(v string) string {
	switch {
	case strings.HasPrefix(v, "'"):
		s, _, _ := strings.Cut(v[1:], "'")
		return s
	case strings.HasPrefix(v, `"`):
		var b strings.Builder
		for i := 1; i < len(v) && v[i] != '"'; i++ {
			if v[i] == '\\' && i+1 < len(v) && strings.IndexByte("\"\\$`", v[i+1]) >= 0 {
				i++
			}
			b.WriteByte(v[i])
		}
		return b.String()
	}
	if end := strings.IndexAny(v, " \t"); end >= 0 {
		return v[:end]
	}
	return v
}

// runlevels are the runlevels of each service: the directories of /etc/runlevels that hold an entry of its name (the
// link rc-update add makes), sorted. The links are not followed: below a test root they point at paths that are not
// there. No /etc/runlevels is no runlevels; a directory that cannot be read is an error.
func (r Reader) runlevels() (map[string][]string, error) {
	levels, err := readDir(filepath.Join(r.Root, runlevelDir))
	if err != nil {
		return nil, err
	}
	in := map[string][]string{}
	for _, level := range levels { // sorted by name
		if !level.IsDir() {
			continue
		}
		entries, err := readDir(filepath.Join(r.Root, runlevelDir, level.Name()))
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			in[e.Name()] = append(in[e.Name()], level.Name())
		}
	}
	return in, nil
}
