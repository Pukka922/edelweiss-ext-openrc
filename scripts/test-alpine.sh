#!/bin/sh
# make test-alpine: runs the OpenRC tests (integration/, go test -tags integration) inside throwaway Alpine containers,
# never on this machine. The test binary is built here (static, CGO_ENABLED=0); each scenario runs in a container of its
# own (docker run --rm --init alpine:<tag>, without extra privileges), which is removed afterwards. The container first
# installs openrc with apk from the Alpine mirrors (its only network access), then runs setup.sh, which this script
# writes: it installs the test services of integration/testdata/init.d into /etc/init.d (mode 0755), creates
# /run/openrc/softlevel (the file the extension takes for "OpenRC is present"; an empty one is enough for rc-service to
# start and stop services, and there is no booted OpenRC: the tests start the services they need, and those about a
# runlevel change run openrc themselves), caches the service dependencies (rc-update -u: a host has them cached, so no
# command prints the caching) and starts busybox syslogd, which writes /var/log/messages. Then the test runs, as root
# (run.sh), or as nobody (user.sh: the view of a user without admin mode, after the services it reads were started as
# root). --init is needed: a daemon that is killed would stay a zombie without a process that reaps it, and OpenRC would
# still find its process (its stop then fails after 5 s, "1 process refused to stop").
# A scenario passes only when docker exits 0 and its output has the "--- PASS: <name>" line, so a renamed test cannot
# pass silently. The script fails when no scenario ran.
# TESTS=<text> runs only the scenarios whose name contains it.
set -eu
ROOT=$(cd "$(dirname "$0")/.." && pwd)
WORK=$(mktemp -d)
OUT=$(mktemp -d)
trap 'rm -rf "$WORK" "$OUT"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
GO=${GO:-go}
(cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH="$("$GO" env GOARCH)" "$GO" test -c -tags integration \
  -o "$WORK/integration.test" ./integration)
mkdir "$WORK/init.d"
cp "$ROOT"/integration/testdata/init.d/* "$WORK/init.d/"
cat >"$WORK/setup.sh" <<'EOF'
#!/bin/sh
set -eu
for f in /t/init.d/*; do
  install -m 0755 "$f" "/etc/init.d/$(basename "$f")"
done
mkdir -p /run/openrc
touch /run/openrc/softlevel
rc-update -u >/dev/null 2>&1
syslogd
EOF
# run.sh TEST: the test as root.
cat >"$WORK/run.sh" <<'EOF'
#!/bin/sh
exec /t/integration.test -test.run "^$1\$" -test.v -test.count=1 -test.timeout 5m
EOF
# user.sh TEST: the test as nobody (EDELWEISS_ALPINE_TEST=user lets it run without root). What TestReadAsUser reads is
# made as root first: t-daemon and t-needs started, t-crash started and its daemon killed (and reaped by --init).
cat >"$WORK/user.sh" <<'EOF'
#!/bin/sh
set -eu
for s in t-daemon t-needs t-crash; do
  rc-service "$s" start
done
pid=$(cat /run/t-crash.pid)
kill -9 "$pid"
i=0
while [ -e "/proc/$pid" ]; do
  i=$((i + 1))
  if [ "$i" -gt 100 ]; then
    echo "user.sh: the killed daemon of t-crash (pid $pid) is still there after 10 s" >&2
    exit 1
  fi
  sleep 0.1
done
exec su -s /bin/sh nobody -c \
  'EDELWEISS_ALPINE_TEST=user exec /t/integration.test -test.run "^$0\$" -test.v -test.count=1 -test.timeout 5m' "$1"
EOF
chmod 0755 "$WORK" "$WORK/init.d" "$WORK/integration.test" "$WORK/setup.sh" "$WORK/run.sh" "$WORK/user.sh"
chmod 0644 "$WORK"/init.d/*
failed=0
ran=0
# scenario IMAGE TEST [SCRIPT]: one test in a fresh container, run by SCRIPT (run.sh when none is given); the test name
# reaches the container's shell as $1, the script as $2
scenario() {
  case "$2" in *"${TESTS:-}"*) ;; *) return 0 ;; esac
  ran=$((ran + 1))
  printf '== %s on %s\n' "$2" "$1"
  rm -f "$OUT/rc"
  { docker run --rm --init -e EDELWEISS_ALPINE_TEST=1 -v "$WORK:/t:ro" "$1" /bin/sh -c \
    'apk add --no-cache --quiet openrc >/dev/null && /t/setup.sh && exec "/t/$2" "$1"' _ "$2" "${3:-run.sh}" \
    && echo 0 >"$OUT/rc" || echo "$?" >"$OUT/rc"; } 2>&1 | tee "$OUT/log"
  if [ "$(cat "$OUT/rc" 2>/dev/null)" != 0 ] || ! grep -q "^--- PASS: $2 (" "$OUT/log"; then
    printf '== FAILED: %s on %s\n' "$2" "$1" >&2
    failed=1
  fi
}
scenario alpine:3.24 TestCapture
scenario alpine:3.22 TestCapture
scenario alpine:3.24 TestRunlevelChange
scenario alpine:3.22 TestRunlevelChange
scenario alpine:3.24 TestRcStatusStates
scenario alpine:3.22 TestRcStatusStates
scenario alpine:3.24 TestActionsAgainstOpenRC
scenario alpine:3.22 TestActionsAgainstOpenRC
scenario alpine:3.24 TestTimeoutAgainstOpenRC
scenario alpine:3.22 TestTimeoutAgainstOpenRC
scenario alpine:3.24 TestLogsAgainstOpenRC
scenario alpine:3.22 TestLogsAgainstOpenRC
scenario alpine:3.24 TestReadAsUser user.sh
scenario alpine:3.22 TestReadAsUser user.sh
if [ "$ran" -eq 0 ]; then
  echo "test-alpine: no scenario matches TESTS=${TESTS:-}" >&2
  exit 1
fi
exit "$failed"
