package policy

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/openrc"
)

func svc(name string, state openrc.State, levels ...string) openrc.Service {
	return openrc.Service{Name: name, State: state, Runlevels: levels}
}

// started is a running service of the default runlevel.
func started(name string) openrc.Service { return svc(name, openrc.Started, "default") }

func TestClassOf(t *testing.T) {
	lists := map[Class][]string{
		Edelweiss: {"edelweiss"},
		Base: {"root", "localmount", "fsck", "sysfs", "procfs", "devfs", "dmesg", "mdev", "mdevd", "mdevd-init", "udev",
			"udev-trigger", "udev-settle", "modloop", "modules", "hwdrivers", "hwclock", "swclock", "bootmisc", "killprocs",
			"mount-ro", "savecache", "cgroups", "loopback", "seedrng", "urandom", "machine-id"},
		Remote: {"sshd", "dropbear", "tailscale", "zerotier-one", "openvpn", "openvpn.client", "openvpn.wg0", "wg-quick",
			"wg-quick.wg0", "wg-quick.home"},
		Network:  {"networking", "dhcpcd", "iwd", "wpa_supplicant", "NetworkManager", "networkmanager", "net.eth0", "net.lo", "net.wlan0.5"},
		Firewall: {"iptables", "ip6tables", "nftables", "awall", "ufw", "firewalld"},
		Storage: {"mdadm-raid", "dmcrypt", "netmount", "edelweiss-storage", "lvm", "zfs-import", "zfs-mount", "zfs-share",
			"zfs-zed", "zfs-load-key", "zfs-x"},
		Free: {"docker", "nginx", "chronyd", "samba", "local", "acpid", "crond",
			// near misses: a list match is exact, only net.*, openvpn.*, wg-quick.* and zfs-* are prefixes, and names
			// are case sensitive
			"network", "net", "netdata", "nettop", "Net.eth0", "NET.eth0", "xnet.eth0", "my-net.lo", "SSHD", "sshd2", "dropbear-x", "NetworkManager-wait-online", "NETWORKMANAGER", "Networkmanager", "networkmanager-wait-online",
			"iptables-persistent", "lvm2", "edelweiss-ext", "edelweiss2", "Edelweiss", "edelweiss-storage2", "localmount2", "Root",
			"tailscaled", "tailscale2", "zerotier", "zerotier-one2", "openvpn2", "openvpn-client", "my-openvpn.x", "wg-quick2",
			"wg-quick-wg0", "wireguard", "Tailscale", "ufw2", "firewalld-x", "zfs", "zfs.import", "zfs_mount", "xzfs-import",
			"ZFS-import"},
	}
	for class, names := range lists {
		for _, name := range names {
			if got := ClassOf(svc(name, openrc.Stopped, "default")); got != class {
				t.Errorf("ClassOf(%s) = %q, want %q", name, got, class)
			}
			if got := ClassOf(openrc.Service{Name: name}); got != class {
				t.Errorf("ClassOf(%s) without runlevels = %q, want %q", name, got, class)
			}
		}
	}

	for _, tc := range []struct {
		name   string
		levels []string
		want   Class
	}{
		// every service of sysinit or shutdown is base, whatever its name
		{"something", []string{"sysinit"}, Base},
		{"something", []string{"shutdown"}, Base},
		{"something", []string{"boot", "sysinit"}, Base},
		{"something", []string{"default", "shutdown"}, Base},
		{"docker", []string{"sysinit"}, Base},
		{"mdev", []string{"sysinit"}, Base},
		// base is above the other lists, edelweiss above everything
		{"sshd", []string{"sysinit"}, Base},
		{"net.eth0", []string{"shutdown"}, Base},
		{"lvm", []string{"sysinit"}, Base},
		{"edelweiss", []string{"sysinit"}, Edelweiss},
		{"edelweiss", []string{"shutdown", "default"}, Edelweiss},
		// the other runlevels do not make a base service, and a base name stays base without a runlevel
		{"something", []string{"default"}, Free},
		{"something", []string{"boot"}, Free},
		{"something", []string{"boot", "default"}, Free},
		{"something", []string{"nonetwork", "single"}, Free},
		{"something", []string{"Sysinit", "sysinit2", "shutdown2"}, Free},
		{"localmount", []string{"boot"}, Base},
		{"sshd", []string{"default"}, Remote},
	} {
		if got := ClassOf(svc(tc.name, openrc.Started, tc.levels...)); got != tc.want {
			t.Errorf("ClassOf(%s in %v) = %q, want %q", tc.name, tc.levels, got, tc.want)
		}
	}
}

// table is the class x action table of the protection rules, typed in again here.
var table = []struct {
	class                                 Class
	names                                 []string
	start, restart, stop, disable, enable Verdict
}{
	{Edelweiss, []string{"edelweiss"}, Deny, Allow, Deny, Deny, Deny},
	{Base, []string{"localmount", "devfs", "hwclock", "seedrng"}, Deny, Deny, Deny, Deny, Deny},
	{Remote, []string{"sshd", "dropbear", "tailscale", "zerotier-one", "openvpn", "openvpn.client", "wg-quick", "wg-quick.wg0"},
		Allow, Warn, Warn, Warn, Allow},
	{Network, []string{"networking", "net.eth0", "dhcpcd", "iwd", "wpa_supplicant", "NetworkManager", "networkmanager"}, Allow, Warn, Warn, Warn, Allow},
	{Firewall, []string{"iptables", "ip6tables", "nftables", "awall", "ufw", "firewalld"}, Warn, Warn, Warn, Warn, Warn},
	{Storage, []string{"mdadm-raid", "dmcrypt", "netmount", "edelweiss-storage", "lvm", "zfs-import", "zfs-mount", "zfs-zed"},
		Allow, Warn, Warn, Warn, Allow},
	{Free, []string{"docker", "nginx"}, Allow, Allow, Allow, Allow, Allow},
}

func TestMatrix(t *testing.T) {
	for _, row := range table {
		want := map[Action]Verdict{Start: row.start, Restart: row.restart, Stop: row.stop, Disable: row.disable, Enable: row.enable}
		for _, name := range row.names {
			s := started(name)
			if got := ClassOf(s); got != row.class {
				t.Errorf("ClassOf(%s) = %q, want %q", name, got, row.class)
			}
			if got := Allowed(s); !reflect.DeepEqual(got, want) {
				t.Errorf("Allowed(%s):\n got %v\nwant %v", name, got, want)
			}
			for a, v := range want {
				// the reason is the class of a warning or a denial, nothing for an allowed action
				reason := string(row.class)
				if v == Allow {
					reason = ""
				}
				if got, wantD := Decide(s, a, nil), (Decision{Verdict: v, Reason: reason}); !reflect.DeepEqual(got, wantD) {
					t.Errorf("Decide(%s, %s) = %+v, want %+v", name, a, got, wantD)
				}
			}
		}
	}
}

func TestAllowedStructure(t *testing.T) {
	// disable of a service that is in no runlevel has nothing to switch off: deny, and no class decided it
	for _, name := range []string{"docker", "sshd", "networking", "nftables", "lvm"} {
		s := svc(name, openrc.Stopped)
		if got := Allowed(s)[Disable]; got != Deny {
			t.Errorf("Allowed(%s, no runlevel)[disable] = %q, want deny", name, got)
		}
		if got, want := Decide(s, Disable, nil), (Decision{Verdict: Deny}); !reflect.DeepEqual(got, want) {
			t.Errorf("Decide(%s, disable) in no runlevel = %+v, want %+v", name, got, want)
		}
		// everything else stays as the class says
		if got := Allowed(s)[Enable]; got == Deny {
			t.Errorf("Allowed(%s, no runlevel)[enable] = deny", name)
		}
	}
	if got := Allowed(openrc.Service{Name: "docker", Runlevels: []string{}})[Disable]; got != Deny {
		t.Errorf("disable with an empty runlevel list = %q, want deny", got)
	}
	if got := Allowed(svc("docker", openrc.Started, "boot"))[Disable]; got != Allow {
		t.Errorf("disable of a service in boot = %q, want allow", got)
	}
	// a denied class keeps its own reason
	if got, want := Decide(svc("edelweiss", openrc.Started), Disable, nil), (Decision{Verdict: Deny, Reason: "edelweiss"}); !reflect.DeepEqual(got, want) {
		t.Errorf("Decide(edelweiss, disable) in no runlevel = %+v, want %+v", got, want)
	}
	// a firewall service in no runlevel cannot be switched off with a warning either
	if got := Allowed(svc("iptables", openrc.Stopped))[Disable]; got != Deny {
		t.Errorf("Allowed(iptables, no runlevel)[disable] = %q, want deny", got)
	}

	// enable of a sysinit or shutdown service: no switch, whatever the name says
	for _, levels := range [][]string{{"sysinit"}, {"shutdown"}, {"sysinit", "shutdown"}, {"boot", "sysinit"}} {
		for _, name := range []string{"docker", "sshd", "nftables", "something"} {
			s := svc(name, openrc.Started, levels...)
			if got, want := Decide(s, Enable, nil), (Decision{Verdict: Deny, Reason: "base"}); !reflect.DeepEqual(got, want) {
				t.Errorf("Decide(%s in %v, enable) = %+v, want %+v", name, levels, got, want)
			}
			if got := Allowed(s)[Enable]; got != Deny {
				t.Errorf("Allowed(%s in %v)[enable] = %q, want deny", name, levels, got)
			}
		}
	}

	// the five actions, always
	want := []Action{Start, Restart, Stop, Disable, Enable}
	got := Allowed(started("docker"))
	if len(got) != len(want) {
		t.Errorf("Allowed has %d entries, want %d: %v", len(got), len(want), got)
	}
	for _, a := range want {
		if _, ok := got[a]; !ok {
			t.Errorf("Allowed has no entry for %s", a)
		}
	}
	// the caller gets its own map
	got[Stop] = Deny
	if again := Allowed(started("docker"))[Stop]; again != Allow {
		t.Errorf("Allowed shares its map: stop = %q after a caller changed it", again)
	}
	if b, _ := json.Marshal(Allowed(started("sshd"))); string(b) != `{"disable":"warn","enable":"allow","restart":"warn","start":"allow","stop":"warn"}` {
		t.Errorf("JSON %s", b)
	}
}

func TestUnknownAction(t *testing.T) {
	for _, a := range []Action{"", "reload", "Stop", "delete", "status"} {
		if got, want := Decide(started("docker"), a, nil), (Decision{Verdict: Deny}); !reflect.DeepEqual(got, want) {
			t.Errorf("Decide(docker, %q) = %+v, want %+v (an unknown action is denied)", a, got, want)
		}
	}
}

func TestDecisionJSON(t *testing.T) {
	for _, tc := range []struct {
		d    Decision
		want string
	}{
		{Decision{Verdict: Allow}, `{"verdict":"allow"}`},
		{Decision{Verdict: Allow, Along: []string{"nginx"}}, `{"verdict":"allow","along":["nginx"]}`},
		{Decision{Verdict: Warn, Reason: "network", Along: []string{"docker", "sshd"}}, `{"verdict":"warn","reason":"network","along":["docker","sshd"]}`},
		{Decision{Verdict: Deny, Reason: "edelweiss", Blocker: "edelweiss"}, `{"verdict":"deny","reason":"edelweiss","blocker":"edelweiss"}`},
	} {
		if b, err := json.Marshal(tc.d); err != nil || string(b) != tc.want {
			t.Errorf("JSON of %+v = %s, %v, want %s", tc.d, b, err, tc.want)
		}
	}
	// the values the API and the UI exchange
	for got, want := range map[string]string{
		string(Allow): "allow", string(Warn): "warn", string(Deny): "deny",
		string(Start): "start", string(Stop): "stop", string(Restart): "restart", string(Enable): "enable", string(Disable): "disable",
		string(Edelweiss): "edelweiss", string(Base): "base", string(Remote): "remote", string(Network): "network",
		string(Firewall): "firewall", string(Storage): "storage", string(Free): "free",
	} {
		if got != want {
			t.Errorf("constant %q, want %q", got, want)
		}
	}
}
