// Package policy decides, for every service and action, whether it is allowed, needs the red warning or is denied: what
// keeps someone from locking themselves out of the machine (sshd, the network, edelweiss itself) or from taking the
// base of the system down. It knows nothing of OpenRC besides the openrc.Service it is given.
package policy

import (
	"slices"
	"strings"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/openrc"
)

// Class is the protection class of a service.
type Class string

// The classes; the string is the Reason of a decision.
const (
	Edelweiss Class = "edelweiss" // edelweiss itself: restart only
	Base      Class = "base"      // the services the system stands on: read only
	Remote    Class = "remote"    // the way in from outside (sshd, a VPN)
	Network   Class = "network"   // the network, and with it the web interface
	Firewall  Class = "firewall"  // new rules can lock one out
	Storage   Class = "storage"   // file systems and RAIDs
	Free      Class = "free"      // everything else
)

// Action is something to do to a service. Enable and Disable switch its autostart.
type Action string

// The actions.
const (
	Start   Action = "start"
	Stop    Action = "stop"
	Restart Action = "restart"
	Enable  Action = "enable"
	Disable Action = "disable"
)

// actions are all of them, for Allowed.
var actions = []Action{Start, Restart, Stop, Disable, Enable}

// Verdict is what the policy says to an action: Allow, Warn (only with the red warning and a confirmation) or Deny.
type Verdict string

// The verdicts.
const (
	Allow Verdict = "allow"
	Warn  Verdict = "warn"
	Deny  Verdict = "deny"
)

// rank orders the verdicts by strictness.
var rank = map[Verdict]int{Allow: 0, Warn: 1, Deny: 2}

// matrix is the verdict of every class for every action.
var matrix = map[Class]map[Action]Verdict{
	Edelweiss: {Start: Deny, Restart: Allow, Stop: Deny, Disable: Deny, Enable: Deny},
	Base:      {Start: Deny, Restart: Deny, Stop: Deny, Disable: Deny, Enable: Deny},
	Remote:    {Start: Allow, Restart: Warn, Stop: Warn, Disable: Warn, Enable: Allow},
	Network:   {Start: Allow, Restart: Warn, Stop: Warn, Disable: Warn, Enable: Allow},
	Firewall:  {Start: Warn, Restart: Warn, Stop: Warn, Disable: Warn, Enable: Warn},
	Storage:   {Start: Allow, Restart: Warn, Stop: Warn, Disable: Warn, Enable: Allow},
	Free:      {Start: Allow, Restart: Allow, Stop: Allow, Disable: Allow, Enable: Allow},
}

// prefixes are the classes of the names that start with a prefix: the instances of a script through a link to it
// (net.eth0 of net.lo, openvpn.<config>, wg-quick.<interface>), and the services of ZFS (zfs-import, zfs-mount …).
var prefixes = []struct {
	prefix string
	class  Class
}{
	{"net.", Network},
	{"openvpn.", Remote},
	{"wg-quick.", Remote},
	{"zfs-", Storage},
}

// ClassOf is the protection class of a service. Edelweiss comes first, then base (every service of the sysinit or
// shutdown runlevel, and the base names), then the lists of the other classes; a list matches the exact name, except
// the prefixes (net.*, openvpn.*, wg-quick.*, zfs-*). Everything else is free.
func ClassOf(s openrc.Service) Class {
	if s.Name == "edelweiss" {
		return Edelweiss
	}
	if slices.ContainsFunc(s.Runlevels, func(l string) bool { return l == "sysinit" || l == "shutdown" }) {
		return Base
	}
	switch s.Name {
	case "root", "localmount", "fsck", "sysfs", "procfs", "devfs", "dmesg", "mdev", "mdevd", "mdevd-init", "udev",
		"udev-trigger", "udev-settle", "modloop", "modules", "hwdrivers", "hwclock", "swclock", "bootmisc", "killprocs",
		"mount-ro", "savecache", "cgroups", "loopback", "seedrng", "urandom", "machine-id":
		return Base
	case "sshd", "dropbear", "tailscale", "zerotier-one", "openvpn", "wg-quick":
		return Remote
	case "networking", "dhcpcd", "iwd", "wpa_supplicant", "NetworkManager", "networkmanager":
		return Network
	case "iptables", "ip6tables", "nftables", "awall", "ufw", "firewalld":
		return Firewall
	case "mdadm-raid", "dmcrypt", "netmount", "edelweiss-storage", "lvm":
		return Storage
	}
	for _, p := range prefixes {
		if strings.HasPrefix(s.Name, p.prefix) {
			return p.class
		}
	}
	return Free
}

// Decision is the policy's answer to an action.
type Decision struct {
	Verdict Verdict `json:"verdict"`
	// Reason is the class that decided a warning or a denial. It is empty for an allowed action and for a denial that
	// no class decided (an unknown action, a disable of a service in no runlevel).
	Reason string `json:"reason,omitempty"`
	// Along are the services that stop along (stop, restart; see goesDown), by name; not for a denial.
	Along   []string `json:"along,omitempty"`
	Blocker string   `json:"blocker,omitempty"` // the dependent that makes it a denial
}

// own decides an action on one service, without what depends on it. An action the matrix does not know is denied, and
// so is disabling a service that is in no runlevel (there is nothing to switch off).
func own(s openrc.Service, a Action) Decision {
	c := ClassOf(s)
	v, known := matrix[c][a]
	switch {
	case !known:
		return Decision{Verdict: Deny}
	case v == Deny:
		return Decision{Verdict: Deny, Reason: string(c)}
	case a == Disable && len(s.Runlevels) == 0:
		return Decision{Verdict: Deny}
	case v == Warn:
		return Decision{Verdict: Warn, Reason: string(c)}
	}
	return Decision{Verdict: Allow}
}

// Allowed is what the policy says to each action on a service alone, without what depends on it: the matrix for the
// list. Enabling a service of sysinit or shutdown is denied (it is base), and so is disabling one that is in no
// runlevel. A new map on every call.
func Allowed(s openrc.Service) map[Action]Verdict {
	m := make(map[Action]Verdict, len(actions))
	for _, a := range actions {
		m[a] = own(s, a).Verdict
	}
	return m
}

// Decide decides an action on target. Stop and restart also stop the services that need it (dependents, transitively,
// as Reader.Dependents lists them): the decision is the strictest of the target and of those that go down with it
// (started, one-shot ones too, starting, crashed or inactive: see goesDown), judged as a stop.
// Among equals the target decides and then the first dependent by name. A dependent of class edelweiss or base makes it
// a denial that names it as Blocker, edelweiss first, with its class as Reason. Start, enable and disable ignore the
// dependents.
func Decide(target openrc.Service, a Action, dependents []openrc.Service) Decision {
	d := own(target, a)
	if d.Verdict == Deny || (a != Stop && a != Restart) {
		return d
	}
	along := running(target.Name, dependents)
	blocker := ""
	for _, dep := range along {
		if o := own(dep, Stop); worse(o, d) {
			d, blocker = o, dep.Name
		}
	}
	if d.Verdict == Deny {
		return Decision{Verdict: Deny, Reason: d.Reason, Blocker: blocker}
	}
	for _, dep := range along {
		d.Along = append(d.Along, dep.Name)
	}
	return d
}

// worse reports whether o is stricter than d. Of two denials, one because of edelweiss itself is the stricter: it is the
// one to tell.
func worse(o, d Decision) bool {
	if o.Verdict != d.Verdict {
		return rank[o.Verdict] > rank[d.Verdict]
	}
	return o.Verdict == Deny && o.Reason == string(Edelweiss)
}

// goesDown reports whether a service in this state is stopped along with what it needs. OpenRC stops the dependents
// that are started (a one-shot service too; crashed, which OpenRC keeps in its started state) or inactive; starting
// counts as well, which only makes the decision stricter. A stopped, stopping or failed service stops nothing and counts
// for nothing.
func goesDown(s openrc.State) bool {
	return s == openrc.Started || s == openrc.Starting || s == openrc.Crashed || s == openrc.Inactive
}

// running are the dependents that go down with the target, sorted by name, each once, the target itself never.
func running(target string, dependents []openrc.Service) []openrc.Service {
	seen := map[string]bool{target: true}
	var out []openrc.Service
	for _, dep := range dependents {
		if goesDown(dep.State) && !seen[dep.Name] {
			seen[dep.Name] = true
			out = append(out, dep)
		}
	}
	slices.SortFunc(out, func(a, b openrc.Service) int { return strings.Compare(a.Name, b.Name) })
	return out
}
