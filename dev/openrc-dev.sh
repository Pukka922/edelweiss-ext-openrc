#!/bin/sh
# OpenRC with test services in the edelweiss dev container, for trying the Services page in the browser. Run it in the
# edelweiss checkout while the dev environment runs (make dev there), as root in the container:
#
#   docker compose -f dev/compose.yaml exec -T edelweiss sh -s < ../edelweiss-ext-openrc/dev/openrc-dev.sh
#
# It installs openrc, writes the test services of integration/testdata/init.d but t-hang (the same scripts, as
# heredocs: the container does not see this repository) and three dummies whose description says "(dev dummy)":
# edelweiss, sshd and networking. Their start and stop do nothing, so the page shows the protection of these classes
# without touching the real edelweissd (the PID 1 of the container, not started by OpenRC) or the network of the
# container. It marks OpenRC as present (/run/openrc/softlevel), starts busybox syslogd and brings the services into
# the states the page shows:
#
#   started            edelweiss, networking, sshd (one-shot dummies), t-daemon, t-needs (needs t-daemon), t-log
#   crashed            t-crash
#   failed             t-fail: its start failed, which the system log tells
#   autostart, idle    t-oneshot (boot)
#   stopped            t-super
#   default runlevel   edelweiss, networking, sshd, t-daemon, t-fail
#
# It refuses to run anywhere but in the edelweiss dev container (see guard): on a host it would replace sshd, networking
# and edelweiss with dummies and lock the owner out at the next boot. It changes the dev container only, which make dev
# recreates. It can run again: it starts the services that should run, adds the autostart links that are missing and
# makes t-crash crash again; it stops nothing, takes nothing out of the autostart and adds no second line to the system
# log.
#
# The dev container has no init that reaps processes: a daemon that ends stays a zombie of edelweissd, and OpenRC counts
# a zombie as running. Therefore, unlike on a host:
#   - /etc/conf.d/<service> of the daemons sets retry="0": a stop sends SIGTERM and does not wait for the process to be
#     gone; it would wait 5 s for the zombie and then fail ("1 process refused to stop");
#   - t-crash crashes by a pid file that names a process that is gone: its killed daemon stays a zombie, which OpenRC
#     would still find;
#   - t-super (supervise-daemon) is left stopped: its stop waits for the supervisor to be gone, which never happens in
#     this container, and holds the job lock until the 5-minute limit. Start it to see a supervised service, but do not
#     stop or restart it here;
#   - t-hang, whose start never ends, is not written at all: a click on its start would hold the job lock for the whole
#     5-minute limit, and the processes the timeout kills would stay zombies of edelweissd (the container tests show
#     the timeout, in a few seconds).
# The rest of the page (states, logs, protection, the autostart) behaves as on a host.
set -eu

# guard: ends the script, having changed nothing, unless it runs in the edelweiss dev container: a container
# (/.dockerenv or /run/.containerenv) whose PID 1 is edelweissd (the entrypoint of the dev container execs it).
guard() {
  if [ ! -e /.dockerenv ] && [ ! -e /run/.containerenv ]; then
    echo "openrc-dev: this is no container (neither /.dockerenv nor /run/.containerenv exists): nothing done" >&2
    exit 1
  fi
  pid1=$(cat /proc/1/comm 2>/dev/null) || pid1=
  if [ "$pid1" != edelweissd ]; then
    echo "openrc-dev: PID 1 is ${pid1:-unknown}, not edelweissd as in the edelweiss dev container:" \
      "nothing done" >&2
    exit 1
  fi
}

# status SERVICE: the exit status of rc-service SERVICE status: 0 started, 3 stopped, 32 crashed
status() {
  if rc-service "$1" status >/dev/null 2>&1; then echo 0; else echo "$?"; fi
}

# put FILE: stdin into FILE (0755), written anew by every run
put() {
  cat >"$1"
  chmod 0755 "$1"
}

main() {
  guard
  apk add --no-cache --quiet openrc

  # /sys/fs/cgroup is read-only in a container: "none" makes OpenRC skip its cgroups, and with them the mkdir errors
  # that every start and stop prints there otherwise (as edelweiss's dev/service-test.sh does).
  grep -q '^rc_cgroup_mode="none"$' /etc/rc.conf ||
    printf 'rc_cgroup_mode="none"\nrc_controller_cgroups="NO"\n' >>/etc/rc.conf

  # The test services of integration/testdata/init.d.
  put /etc/init.d/t-crash <<'EOF'
#!/sbin/openrc-run
description="Test daemon that the capture kills"
command=/bin/sleep
command_args=100000
command_background=true
pidfile=/run/t-crash.pid
EOF
  put /etc/init.d/t-daemon <<'EOF'
#!/sbin/openrc-run
description="Test daemon: sleeps in the background"
command=/bin/sleep
command_args=100000
command_background=true
pidfile=/run/t-daemon.pid
EOF
  put /etc/init.d/t-fail <<'EOF'
#!/sbin/openrc-run
description="Test service whose start fails"

start() {
	eerror "boom: port 80 in use"
	return 1
}
EOF
  put /etc/init.d/t-log <<'EOF'
#!/sbin/openrc-run
description="Test daemon that writes output_log and error_log"
command=/bin/sh
command_args="-c 'while :; do echo tick; sleep 1; done'"
command_background=true
pidfile=/run/t-log.pid
output_log=/var/log/t-log.log
error_log=/var/log/t-log.err
EOF
  put /etc/init.d/t-needs <<'EOF'
#!/sbin/openrc-run
description="Test daemon that needs t-daemon"
command=/bin/sleep
command_args=100000
command_background=true
pidfile=/run/t-needs.pid

depend() {
	need t-daemon
}
EOF
  put /etc/init.d/t-oneshot <<'EOF'
#!/sbin/openrc-run
description="Test one-shot service without a daemon"

start() {
	ebegin "one shot"
	eend 0
}
EOF
  put /etc/init.d/t-super <<'EOF'
#!/sbin/openrc-run
description="Test daemon under supervise-daemon"
supervisor=supervise-daemon
command=/bin/sleep
command_args=100000
EOF
  for s in t-crash t-daemon t-log t-needs; do
    printf '# dev container only (dev/openrc-dev.sh): stop without waiting for the daemon to be reaped\nretry="0"\n' \
      >"/etc/conf.d/$s"
  done

  # The dummies: edelweiss (class edelweiss: restart only), sshd (remote) and networking (network, which sshd needs as
  # on a host). They replace the networking script of the openrc package.
  put /etc/init.d/edelweiss <<'EOF'
#!/sbin/openrc-run
# dev/openrc-dev.sh: a dummy for the edelweiss dev container, whose edelweissd is not started by OpenRC. Start and stop
# do nothing; above all they never stop or signal the real edelweissd.
description="edelweiss web administration (dev dummy)"

depend() {
	use net
}

start() {
	ebegin "Starting the dummy edelweiss (the real edelweissd is not touched)"
	eend 0
}

stop() {
	ebegin "Stopping the dummy edelweiss (the real edelweissd is not touched)"
	eend 0
}
EOF
  put /etc/init.d/sshd <<'EOF'
#!/sbin/openrc-run
# dev/openrc-dev.sh: a dummy for the edelweiss dev container; there is no sshd. Start and stop do nothing.
description="OpenSSH server (dev dummy)"

depend() {
	need net
}

start() {
	ebegin "Starting the dummy sshd"
	eend 0
}

stop() {
	ebegin "Stopping the dummy sshd"
	eend 0
}
EOF
  put /etc/init.d/networking <<'EOF'
#!/sbin/openrc-run
# dev/openrc-dev.sh: a dummy for the edelweiss dev container. Start and stop do nothing to the network.
description="Network interfaces (dev dummy)"

depend() {
	provide net
}

start() {
	ebegin "Starting the dummy networking"
	eend 0
}

stop() {
	ebegin "Stopping the dummy networking"
	eend 0
}
EOF

  # OpenRC is present; the dependencies are cached now, so that no answer of the page starts with the caching.
  mkdir -p /run/openrc
  touch /run/openrc/softlevel
  rc-update -u >/dev/null

  # The system log, before the services start: busybox syslogd writes /var/log/messages, as on an Alpine host.
  pgrep -x syslogd >/dev/null || syslogd

  for s in edelweiss networking sshd t-daemon t-fail; do
    [ -e "/etc/runlevels/default/$s" ] || rc-update add "$s" default
  done
  [ -e /etc/runlevels/boot/t-oneshot ] || rc-update add t-oneshot boot

  for s in edelweiss networking sshd t-daemon t-needs t-log; do
    [ "$(status "$s")" = 0 ] || rc-service "$s" start
  done

  # A line of t-daemon in the system log: its system log source on the page has nothing else.
  grep -q ' t-daemon: ' /var/log/messages 2>/dev/null || logger -t t-daemon "hello from t-daemon (dev)"

  # t-crash crashes: its daemon is killed, and its pid file names a process that is gone (started and waited for here).
  if [ "$(status t-crash)" != 32 ]; then
    [ "$(status t-crash)" = 0 ] || rc-service t-crash start
    kill -9 "$(cat /run/t-crash.pid)"
    sh -c : &
    gone=$!
    wait "$gone"
    echo "$gone" >/run/t-crash.pid
  fi

  # t-fail fails to start: OpenRC leaves it stopped, and the failure in the system log makes it failed on the page.
  # Once: a second run adds no second failure.
  if ! grep -q 'ERROR: t-fail failed to start' /var/log/messages 2>/dev/null; then
    echo "openrc-dev: t-fail is started to fail:"
    if rc-service t-fail start; then
      echo "openrc-dev: the start of t-fail worked, it has to fail" >&2
      exit 1
    fi
  fi

  rc-status --all --nocolor
}

# The whole script is read before main runs, and main reads nothing from stdin: sh -s reads the script from stdin.
main </dev/null
