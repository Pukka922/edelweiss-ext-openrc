package policy

import (
	"reflect"
	"testing"

	"github.com/Pukka922/edelweiss-ext-openrc/internal/openrc"
)

// Decide with the services that stop along: the strictest of them decides.

func TestDecideCascade(t *testing.T) {
	networking := started("networking")
	sshd, docker := started("sshd"), started("docker")

	for _, tc := range []struct {
		name       string
		target     openrc.Service
		a          Action
		dependents []openrc.Service
		want       Decision
	}{
		// the strictest wins; on a tie the target, and the services that stop along are sorted
		{"stop networking", networking, Stop, []openrc.Service{sshd, docker},
			Decision{Verdict: Warn, Reason: "network", Along: []string{"docker", "sshd"}}},
		{"restart is like stop", networking, Restart, []openrc.Service{docker, sshd},
			Decision{Verdict: Warn, Reason: "network", Along: []string{"docker", "sshd"}}},
		{"free target, remote dependent", docker, Stop, []openrc.Service{sshd},
			Decision{Verdict: Warn, Reason: "remote", Along: []string{"sshd"}}},
		{"free target, remote dependent, restart", docker, Restart, []openrc.Service{sshd},
			Decision{Verdict: Warn, Reason: "remote", Along: []string{"sshd"}}},
		{"free target, firewall dependent", docker, Stop, []openrc.Service{started("nginx"), started("nftables")},
			Decision{Verdict: Warn, Reason: "firewall", Along: []string{"nftables", "nginx"}}},
		{"first warning by name", docker, Stop, []openrc.Service{started("lvm"), started("nginx"), started("awall")},
			Decision{Verdict: Warn, Reason: "firewall", Along: []string{"awall", "lvm", "nginx"}}},
		{"allow lists what stops along", docker, Stop, []openrc.Service{started("php-fpm"), started("nginx")},
			Decision{Verdict: Allow, Along: []string{"nginx", "php-fpm"}}},
		{"allow, restart", docker, Restart, []openrc.Service{started("nginx")},
			Decision{Verdict: Allow, Along: []string{"nginx"}}},
		{"no dependents", docker, Stop, nil, Decision{Verdict: Allow}},
		{"no dependents, empty list", sshd, Restart, []openrc.Service{}, Decision{Verdict: Warn, Reason: "remote"}},
		{"warning target, free dependents", sshd, Stop, []openrc.Service{docker},
			Decision{Verdict: Warn, Reason: "remote", Along: []string{"docker"}}},
		{"warning target, other warning dependent: the target decides", sshd, Stop, []openrc.Service{started("iptables")},
			Decision{Verdict: Warn, Reason: "remote", Along: []string{"iptables"}}},

		// edelweiss or a base service among them makes it a denial that names it
		{"edelweiss depends on it", networking, Stop, []openrc.Service{sshd, started("edelweiss"), docker},
			Decision{Verdict: Deny, Reason: "edelweiss", Blocker: "edelweiss"}},
		{"edelweiss depends on it, restart", docker, Restart, []openrc.Service{started("edelweiss")},
			Decision{Verdict: Deny, Reason: "edelweiss", Blocker: "edelweiss"}},
		{"a base dependent", networking, Stop, []openrc.Service{docker, started("localmount")},
			Decision{Verdict: Deny, Reason: "base", Blocker: "localmount"}},
		{"a base dependent, restart", docker, Restart, []openrc.Service{started("localmount")},
			Decision{Verdict: Deny, Reason: "base", Blocker: "localmount"}},
		{"a sysinit dependent is base", docker, Stop, []openrc.Service{svc("mything", openrc.Started, "sysinit")},
			Decision{Verdict: Deny, Reason: "base", Blocker: "mything"}},
		{"a shutdown dependent is base", docker, Stop, []openrc.Service{svc("mything", openrc.Started, "shutdown", "default")},
			Decision{Verdict: Deny, Reason: "base", Blocker: "mything"}},
		{"edelweiss is named before a base dependent", docker, Stop, []openrc.Service{started("devfs"), started("edelweiss"), started("bootmisc")},
			Decision{Verdict: Deny, Reason: "edelweiss", Blocker: "edelweiss"}},
		{"the first base dependent by name", docker, Stop, []openrc.Service{started("udev"), sshd, started("devfs"), started("hwclock")},
			Decision{Verdict: Deny, Reason: "base", Blocker: "devfs"}},
		{"a denial beats a warning", sshd, Stop, []openrc.Service{started("localmount")},
			Decision{Verdict: Deny, Reason: "base", Blocker: "localmount"}},

		// a denied target is denied by its own class, whatever depends on it
		{"base target", started("localmount"), Stop, []openrc.Service{docker},
			Decision{Verdict: Deny, Reason: "base"}},
		{"base target restart, edelweiss dependent", started("localmount"), Restart, []openrc.Service{started("edelweiss")},
			Decision{Verdict: Deny, Reason: "base"}},
		{"stop edelweiss", started("edelweiss"), Stop, []openrc.Service{docker},
			Decision{Verdict: Deny, Reason: "edelweiss"}},

		// restarting edelweiss is allowed; what it would stop along still counts
		{"restart edelweiss", started("edelweiss"), Restart, nil, Decision{Verdict: Allow}},
		{"restart edelweiss, free dependent", started("edelweiss"), Restart, []openrc.Service{docker},
			Decision{Verdict: Allow, Along: []string{"docker"}}},
		{"restart edelweiss, itself among its dependents", started("edelweiss"), Restart, []openrc.Service{started("edelweiss")},
			Decision{Verdict: Allow}},
		{"restart edelweiss, running remote dependent", started("edelweiss"), Restart, []openrc.Service{sshd},
			Decision{Verdict: Warn, Reason: "remote", Along: []string{"sshd"}}},
		{"restart edelweiss, running base dependent", started("edelweiss"), Restart, []openrc.Service{docker, started("localmount")},
			Decision{Verdict: Deny, Reason: "base", Blocker: "localmount"}},
		{"restart edelweiss, stopped remote dependent", started("edelweiss"), Restart, []openrc.Service{svc("sshd", openrc.Stopped, "default")},
			Decision{Verdict: Allow}},

		// a crashed service is still started as far as OpenRC is concerned: it stops along and counts
		{"crashed edelweiss dependent", docker, Stop, []openrc.Service{svc("edelweiss", openrc.Crashed, "default")},
			Decision{Verdict: Deny, Reason: "edelweiss", Blocker: "edelweiss"}},
		{"crashed base dependent", docker, Restart, []openrc.Service{svc("localmount", openrc.Crashed, "boot")},
			Decision{Verdict: Deny, Reason: "base", Blocker: "localmount"}},
		{"crashed dependents stop along", docker, Stop, []openrc.Service{svc("nginx", openrc.Crashed, "default"), started("php-fpm")},
			Decision{Verdict: Allow, Along: []string{"nginx", "php-fpm"}}},
		{"crashed remote dependent", docker, Stop, []openrc.Service{svc("sshd", openrc.Crashed, "default")},
			Decision{Verdict: Warn, Reason: "remote", Along: []string{"sshd"}}},
		{"a VPN dependent is a way in", docker, Stop, []openrc.Service{started("nginx"), started("wg-quick.wg0")},
			Decision{Verdict: Warn, Reason: "remote", Along: []string{"nginx", "wg-quick.wg0"}}},
		{"a zfs dependent", docker, Restart, []openrc.Service{started("zfs-mount")},
			Decision{Verdict: Warn, Reason: "storage", Along: []string{"zfs-mount"}}},
		{"stop docker, itself among its dependents", docker, Stop, []openrc.Service{docker, started("nginx")},
			Decision{Verdict: Allow, Along: []string{"nginx"}}},
		{"each dependent once", docker, Stop, []openrc.Service{started("nginx"), started("nginx"), started("php-fpm")},
			Decision{Verdict: Allow, Along: []string{"nginx", "php-fpm"}}},

		// start, enable and disable ignore the dependents
		{"start ignores dependents", networking, Start, []openrc.Service{started("edelweiss"), started("localmount")},
			Decision{Verdict: Allow}},
		{"start free target", docker, Start, []openrc.Service{started("edelweiss")}, Decision{Verdict: Allow}},
		{"start firewall", started("nftables"), Start, []openrc.Service{started("edelweiss")}, Decision{Verdict: Warn, Reason: "firewall"}},
		{"enable ignores dependents", networking, Enable, []openrc.Service{started("edelweiss")}, Decision{Verdict: Allow}},
		{"disable ignores dependents", docker, Disable, []openrc.Service{started("edelweiss")}, Decision{Verdict: Allow}},
		{"disable warns for its own class only", sshd, Disable, []openrc.Service{docker},
			Decision{Verdict: Warn, Reason: "remote"}},
	} {
		got := Decide(tc.target, tc.a, tc.dependents)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: Decide(%s, %s, %v) =\n %+v\nwant %+v", tc.name, tc.target.Name, tc.a, names(tc.dependents), got, tc.want)
		}
	}
}

func names(l []openrc.Service) []string {
	var n []string
	for _, s := range l {
		n = append(n, s.Name)
	}
	return n
}

// Only a dependent that OpenRC stops along goes down with the target: started (a one-shot one too), crashed (OpenRC keeps
// it in its started state), inactive (OpenRC stops the started and the inactive ones that need the target), and starting,
// which only makes the check stricter. OpenRC's needsme lists the others too, and they neither show up in Along nor
// change the verdict.
func TestDecideRunningDependents(t *testing.T) {
	for _, tc := range []struct {
		state   openrc.State
		oneShot bool
		counts  bool
	}{
		{openrc.Started, false, true},
		{openrc.Started, true, true}, // a one-shot started service is running as far as OpenRC is concerned
		{openrc.Starting, false, true},
		{openrc.Stopped, false, false},
		{openrc.Crashed, false, true},
		{openrc.Inactive, false, true},
		{openrc.Failed, false, false},
		{openrc.Stopping, false, false},
		{"", false, false},
	} {
		dep := func(name string) openrc.Service {
			return openrc.Service{Name: name, State: tc.state, OneShot: tc.oneShot, Runlevels: []string{"default"}}
		}
		for _, a := range []Action{Stop, Restart} {
			// an edelweiss dependent denies when it runs, a free one is listed when it runs
			got := Decide(started("docker"), a, []openrc.Service{dep("edelweiss")})
			want := Decision{Verdict: Allow}
			if tc.counts {
				want = Decision{Verdict: Deny, Reason: "edelweiss", Blocker: "edelweiss"}
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s edelweiss dependent (state %q, one-shot %v): %+v, want %+v", a, tc.state, tc.oneShot, got, want)
			}
			got = Decide(started("docker"), a, []openrc.Service{dep("localmount")})
			want = Decision{Verdict: Allow}
			if tc.counts {
				want = Decision{Verdict: Deny, Reason: "base", Blocker: "localmount"}
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s base dependent (state %q, one-shot %v): %+v, want %+v", a, tc.state, tc.oneShot, got, want)
			}
			got = Decide(started("docker"), a, []openrc.Service{dep("sshd"), dep("nginx")})
			want = Decision{Verdict: Allow}
			if tc.counts {
				want = Decision{Verdict: Warn, Reason: "remote", Along: []string{"nginx", "sshd"}}
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s remote dependent (state %q, one-shot %v): %+v, want %+v", a, tc.state, tc.oneShot, got, want)
			}
		}
	}

	// mixed: only those that go down decide and are listed
	got := Decide(started("networking"), Stop, []openrc.Service{
		svc("edelweiss", openrc.Stopped, "default"),
		svc("localmount", openrc.Stopping, "boot"),
		svc("nginx", openrc.Started, "default"),
		svc("php-fpm", openrc.Stopped, "default"),
		svc("chronyd", openrc.Starting, "default"),
		svc("samba", openrc.Crashed, "default"),
		svc("net.eth1", openrc.Inactive, "default"),
		svc("cupsd", openrc.Failed, "default"),
	})
	if want := (Decision{Verdict: Warn, Reason: "network", Along: []string{"chronyd", "net.eth1", "nginx", "samba"}}); !reflect.DeepEqual(got, want) {
		t.Errorf("mixed dependents: %+v, want %+v", got, want)
	}
	// an inactive dependent of the base is stopped along as well: a denial that names it
	if got, want := Decide(started("networking"), Stop, []openrc.Service{svc("localmount", openrc.Inactive, "boot")}),
		(Decision{Verdict: Deny, Reason: "base", Blocker: "localmount"}); !reflect.DeepEqual(got, want) {
		t.Errorf("inactive base dependent: %+v, want %+v", got, want)
	}
	// the target itself needs no particular state: stopping a stopped service is the caller's refusal
	if got := Decide(svc("sshd", openrc.Stopped, "default"), Stop, nil); got.Verdict != Warn {
		t.Errorf("stop of a stopped sshd = %+v, want warn", got)
	}
}

func TestDecideLeavesDependentsAlone(t *testing.T) {
	deps := []openrc.Service{started("sshd"), started("docker"), started("nginx")}
	Decide(started("networking"), Stop, deps)
	if got, want := names(deps), []string{"sshd", "docker", "nginx"}; !reflect.DeepEqual(got, want) {
		t.Errorf("dependents after Decide: %v, want %v", got, want)
	}
}
