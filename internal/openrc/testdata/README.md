# Captured outputs of the real OpenRC

These files are the unchanged output of the real OpenRC tools of Alpine, captured in throwaway containers by
`make test-alpine` (`scripts/test-alpine.sh`; the tests are in `integration/`, the services in
`integration/testdata/init.d`). The parsers of `internal/openrc` and `internal/logs` are tested against them. Do not
edit or reformat them: `needsme.txt`, `ineed.txt` and `rc-update-show.txt` end lines with spaces, the files below
`/run/openrc/options/` have no final newline in the original (see below), and `.editorconfig` keeps editors from touching them.

They come from `alpine:3.24` (OpenRC 0.63.2, busybox 1.37.0). `alpine:3.22` (OpenRC 0.62.6) is captured as well; its
`rc-status`, `rc-status-failed`, `rc-status-states`, `rc-update show`, `needsme`, `ineed`, `start-fail` and syslog lines are
identical, so there are no `…-3.22.txt` files. Its `describe` lists two commands less (`restart` and `reload` have no
entry), and its tools do not print the cgroup line described below when they stop a service.

Every test runs in a container of its own, so a file does not depend on what another test did:

| Test | File | What it holds |
|---|---|---|
| `TestCapture` | `rc-status.txt` | `rc-status --all --nocolor` |
| | `rc-update-show.txt` | `rc-update show --verbose` |
| | `run-openrc.txt` | `find /run/openrc`, `ls -lAR /run/openrc`, the content of every regular file below it (`=== file <path> ===` blocks) and `ls -lAR /etc/runlevels`, each under its `=== name ===` line |
| | `pidfiles.txt` | `/run/t-daemon.pid` (start-stop-daemon) and `/run/supervise-t-super.pid` (supervise-daemon) as `=== file <path> ===` blocks; the other state files of supervise-daemon are `/run/openrc/options/t-super/*` in `run-openrc.txt` (and the FIFO `/run/openrc/supervise-t-super.ctl`, which is no regular file) |
| | `describe.txt` | `rc-service t-needs describe` |
| | `needsme.txt` | `rc-service t-daemon needsme` |
| | `ineed.txt` | `rc-service t-needs ineed` |
| | `start-fail.txt` | `rc-service t-fail start`: the first line is `exit status: <n>`, the rest is the output (stdout and stderr together) |
| | `messages.txt` | `tail -n 20 /var/log/messages` (busybox syslogd) after the services ran |
| `TestRunlevelChange` | `rc-status-failed.txt` | `rc-status --all --nocolor` after `openrc captest`, a runlevel that holds the failing `t-fail`: it is `[  stopped  ]`, not failed |
| `TestRcStatusStates` | `rc-status-states.txt` | `rc-status --all --nocolor` with a service in each of the states starting, inactive and failed |

Things to know when reading them:

- The files below `/run/openrc/options/` have no final newline; the capture adds one to every block that lacks it.
- The container is not a booted system: `/run/openrc/softlevel` is empty, so the current runlevel is `sysinit`, and
  every `rc-service` start, stop and restart prints `mkdir: can't create directory '/sys/fs/cgroup/openrc.<service>':
  Read-only file system` (the cgroup of a docker container is read-only; a real host does not print it). Host name and
  times in `messages.txt` and `run-openrc.txt` are those of the container.
- `messages.txt`: the lines `t-daemon: hello from t-daemon`, `t-fail: an error of t-fail` and `root: no tag` are written
  by `logger` calls of the test; the others are written by syslogd and by OpenRC itself (`supervise-daemon[…]`, `t-fail[…]`).
- In `TestCapture` `t-fail` is added to the `default` runlevel before its start fails, so that `rc-status` lists it while it
  is stopped; a failed start of a service that is in no runlevel leaves no trace in `rc-status`. A failed start never
  made a service `failed`, not even when `openrc <runlevel>` ran it (`rc-status-failed.txt`; `/run/openrc/failed` stayed empty).
- `rc-status-states.txt`: OpenRC does not reach `inactive` or `failed` while the test services run, so the test makes the
  three states with OpenRC's own tools, in services it writes to `/etc/init.d` first. The `starting` line is a real state:
  `t-slow`, whose start takes three seconds, is started in the background while rc-status runs. The `inactive` line comes from
  a marker: `t-inactive` calls `mark_service_inactive` in its start (as the net scripts do; `rc-service t-inactive start` then
  exits 1 with `WARNING: t-inactive has started, but is inactive`). The `failed` line comes from a marker as well: an extra
  command of `t-markfailed` calls `mark_service_failed`, which makes `/run/openrc/failed/t-markfailed`; no start that fails does
  that. The layout of the lines is what OpenRC prints for these markers (`[ starting  ]`, `[ inactive  ]`, `[ failed ]`).

## Recapture

```sh
make test-alpine 2>&1 | tee capture.log
```

The log has a section per test and image (it starts with `== <test> on <image>`), and in it the blocks of the test, each
below its `=== <name> ===` line (`=== end ===` closes the last one). One block, without its marker line, for example
`rc-status.txt`:

```sh
awk -v test=TestCapture -v image=alpine:3.24 -v want=rc-status '
  /^== / { sec = ($0 == "== " test " on " image); on = 0; next }
  sec && /^=== .+ ===$/ { on = ($0 == "=== " want " ==="); next }
  sec && on' capture.log > rc-status.txt
```

`rc-update-show`, `describe`, `needsme`, `ineed`, `start-fail` and `messages` are the blocks of the same names in
`TestCapture`, `rc-status-failed` is the block of that name in `TestRunlevelChange` and `rc-status-states` the one in
`TestRcStatusStates`. The files made of several blocks keep their marker lines; `run-openrc.txt` is made like this (for
`pidfiles.txt` use `-v re='file /run/(t-daemon|supervise-t-super)[.]pid'`):

```sh
awk -v test=TestCapture -v image=alpine:3.24 -v re='run-openrc|run-openrc-ls|file /run/openrc/.*|etc-runlevels' '
  /^== / { sec = ($0 == "== " test " on " image); on = 0; next }
  sec && /^=== .+ ===$/ { on = ($0 ~ ("^=== (" re ") ===$")) }
  sec && on' capture.log > run-openrc.txt
```

The other blocks of the log (`tools`, `status-…`, `start-…`, …) are not committed. The tests fail only when a command does
not run or ends with an unexpected exit status.
