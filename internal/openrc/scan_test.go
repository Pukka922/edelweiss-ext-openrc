package openrc

import (
	"context"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
)

var ctx = context.Background()

func TestValidName(t *testing.T) {
	for _, s := range []string{"sshd", "net.eth0", "php-fpm83", "node_exporter", "c++", "a", "9p", "NetworkManager",
		strings.Repeat("x", 64)} {
		if !ValidName(s) {
			t.Errorf("ValidName(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", ".hidden", "-x", "_x", "+x", "a b", "a/b", "../x", "..", "x\n", "\nx", "x:y", "äpfel",
		"x$y", "x'y", strings.Repeat("x", 65)} {
		if ValidName(s) {
			t.Errorf("ValidName(%q) = true, want false", s)
		}
	}
}

func TestScanInitD(t *testing.T) {
	f := newFixture(t)
	f.openrc()
	long := strings.Repeat("s", 64)
	want := map[string]string{ // name → description
		"sshd":          "OpenSSH server daemon",
		"crond":         `Cron daemon: runs "jobs"`,
		"local":         "Local",
		"docker":        "Docker Daemon manages Containers",
		"verbatim":      `Costs $5 for ${RC_SVCNAME}, "quoted" \n`,
		"indented":      "Indented",
		"unclosed":      "No closing quote",
		"nodesc":        "",
		"net.lo":        "Configures network interfaces",
		"net.eth0":      "Configures network interfaces", // a link to net.lo, kept under its own name
		"net.wlan0":     "Configures network interfaces", // an absolute link, resolved below the root
		"chain":         "Configures network interfaces", // a link to the link net.eth0
		"roundtrip":     "Configures network interfaces", // out of init.d and back into it
		"php-fpm83":     "PHP FastCGI Process Manager",
		"node_exporter": "",
		long:            "Longest name",
	}
	scripts := map[string]string{
		"sshd":  "#!/sbin/openrc-run\n\ndescription=\"OpenSSH server daemon\"\ndescription_checkconfig=\"Verify configuration\"\n",
		"crond": "#!/sbin/openrc-run\ndescription='Cron daemon: runs \"jobs\"'\n",
		"local": "#!/sbin/openrc-run\ndescription=Local # the local.d scripts\n",
		"docker": "#!/sbin/openrc-run\nname=\"Docker Daemon\"\ndescription_reload=\"Reload\"\n" +
			"description=\"Docker Daemon manages Containers\"\ndescription=\"the second is not taken\"\n",
		"verbatim":      "#!/sbin/openrc-run\n" + `description="Costs \$5 for ${RC_SVCNAME}, \"quoted\" \n"` + "\n",
		"indented":      "#!/sbin/openrc-run\n  \tdescription='Indented'\n",
		"unclosed":      "#!/sbin/openrc-run\ndescription=\"No closing quote\n",
		"nodesc":        "#!/sbin/openrc-run\ncommand=/usr/bin/nodesc\n",
		"net.lo":        "#!/sbin/openrc-run\ndescription=\"Configures network interfaces\"\n",
		"php-fpm83":     "#!/sbin/openrc-run\ndescription=\"PHP FastCGI Process Manager\"\n",
		"node_exporter": "#!/sbin/openrc-run\ndescription=\n",
		long:            "#!/sbin/openrc-run\ndescription=\"Longest name\"\n",
	}
	for name, text := range scripts {
		f.script(name, text)
	}
	f.link(initDir+"/net.eth0", "net.lo")
	f.link(initDir+"/net.wlan0", "/etc/init.d/net.lo")
	f.link(initDir+"/chain", "net.eth0")
	f.link(initDir+"/roundtrip", "/usr/share/net")
	f.link("/usr/share/net", "../../etc/init.d/net.lo")

	// Dropped: links that end outside init.d, nowhere, in a loop, at a non-executable file or a directory; files that
	// are not executable; names outside the pattern; directories.
	f.file("/usr/libexec/rc/sh/functions.sh", "#!/bin/sh\n", 0o755)
	f.link(initDir+"/functions.sh", "/usr/libexec/rc/sh/functions.sh")
	f.link(initDir+"/escape", "../../usr/libexec/rc/sh/functions.sh")
	f.link(initDir+"/upwards", "../../../../../../usr/libexec/rc/sh/functions.sh")
	f.link(initDir+"/dangling", "gone")
	f.link(initDir+"/loop", "loop")
	f.file(initDir+"/plain", "#!/sbin/openrc-run\ndescription=\"not executable\"\n", 0o644)
	f.link(initDir+"/plainlink", "plain")
	f.file(initDir+"/subdir/inner", "#!/sbin/openrc-run\n", 0o755)
	f.link(initDir+"/dirlink", "subdir")
	f.link(initDir+"/deeper", "subdir/inner")
	for _, name := range []string{".hidden", "_x", "-x", "has space", "x:y", "äpfel", long + "s"} {
		f.script(name, "#!/sbin/openrc-run\ndescription=\"bad name\"\n")
	}

	r, _ := f.reader("")
	snap, err := r.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range snap.Services {
		names = append(names, s.Name)
		if d, ok := want[s.Name]; ok && s.Description != d {
			t.Errorf("%s: description %q, want %q", s.Name, s.Description, d)
		}
		if s.Script != "/etc/init.d/"+s.Name {
			t.Errorf("%s: script %q, want /etc/init.d/%s", s.Name, s.Script, s.Name)
		}
	}
	if wantNames := slices.Sorted(maps.Keys(want)); !reflect.DeepEqual(names, wantNames) {
		t.Errorf("services\n got %q\nwant %q", names, wantNames)
	}
}

func TestRunlevels(t *testing.T) {
	f := newFixture(t)
	f.openrc()
	f.service("sshd", "OpenSSH", "default", "boot")
	f.service("devfs", "devfs", "sysinit")
	f.service("killprocs", "killprocs", "shutdown")
	f.service("offline", "offline", "nonetwork")
	f.service("free", "free")
	f.dir(runlevelDir + "/empty")
	f.file(runlevelDir+"/stray", "", 0o644) // no runlevel
	r, _ := f.reader("")
	snap, err := r.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{"sshd": {"boot", "default"}, "devfs": {"sysinit"}, "killprocs": {"shutdown"},
		"offline": {"nonetwork"}, "free": {}}
	got := map[string][]string{}
	for _, s := range snap.Services {
		got[s.Name] = s.Runlevels
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("runlevels\n got %q\nwant %q", got, want)
	}

	// The capture: t-oneshot in boot, t-daemon and t-fail in default, the others in none.
	r, _ = fromCapture(t, "rc-status.txt")
	if snap, err = r.Read(ctx); err != nil {
		t.Fatal(err)
	}
	got = map[string][]string{}
	for _, s := range snap.Services {
		got[s.Name] = s.Runlevels
	}
	want = map[string][]string{"t-crash": {}, "t-daemon": {"default"}, "t-fail": {"default"}, "t-log": {}, "t-needs": {},
		"t-oneshot": {"boot"}, "t-super": {}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("runlevels of the capture\n got %q\nwant %q", got, want)
	}

	// No /etc/runlevels: no runlevels, no error.
	f = newFixture(t)
	f.openrc()
	f.service("sshd", "OpenSSH")
	r, _ = f.reader("")
	if snap, err = r.Read(ctx); err != nil || len(snap.Services) != 1 || snap.Services[0].Runlevels == nil ||
		len(snap.Services[0].Runlevels) != 0 {
		t.Errorf("without /etc/runlevels: %+v, %v", snap, err)
	}
}
