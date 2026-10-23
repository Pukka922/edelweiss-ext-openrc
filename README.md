# edelweiss-ext-openrc

> **Note:** This project is vibe coded, for lack of time, and made only for my personal use: to manage my Raspberry Pi
> more comfortably. Anyone is welcome to use it, but at their own risk.

The OpenRC services of Alpine Linux, as an extension of [edelweiss](https://github.com/Pukka922/edelweiss), the web
administration interface for Alpine Linux. Its page **Services** (menu section System) shows the services of the host,
whether they run and their logs, live as well; in admin mode it starts, stops and restarts them and switches whether
they start at boot. The occasion was Docker: restarting a service and finding out why it does not run, without SSH.
Texts are in English and German.

It is one static binary with a Go backend (`main.go`, `routes.go`, `internal/`) and a SolidJS interface (`ui/`), built
with the edelweiss SDKs.
[Developing edelweiss extensions](https://github.com/Pukka922/edelweiss/blob/main/docs/extension-development.md)
explains its parts. The build scopes the style sheet of the extension to its container
(`edelweiss-ext-openrc`); the "Styles" section of `docs/extension-development.md` explains how.

**Status:** version 0.1.0 is not released yet (see First publication).

## What it does

### The page

- **Head:** the number of services, how many of them run and how many have a problem (crashed or start failed), and
  **Reload**.
- **Alert:** every crashed service (OpenRC still has it as started, but its daemon is gone) and every service whose
  start failed stands in red at the top, with the **Logs** button and **Restart** (crashed) or **Start** (failed). A
  failed start says how long ago it failed ("4 min ago", anew with every view) when that is known (see Start failed).
- **Filters:** All, Running, Stopped, In the autostart, and a search over name and description.
- **Table:** each service of `/etc/init.d` with its description (the `description=` of its script, shown as it is
  written), a badge for the protected ones ("this interface", "remote access", "network", "firewall", "storage"), its
  state, its autostart and the buttons **Logs**, **Restart**, **Stop** or **Start**, and **Details**. The states:
  running, started (one-shot) for a started service without a daemon of its own (`networking`, `sysctl`), stopped,
  crashed, start failed, starting …, stopping …, inactive, and in yellow "in the autostart, not running" for a stopped
  service of the autostart that neither crashed nor failed (it may be stopped on purpose; OpenRC cannot tell). A
  service under `supervise-daemon` is marked supervised. While a service starts or stops, or a change of it runs, a
  spinner takes the place of the buttons that change it, in its row and in its alert; **Logs** and **Details** stay,
  so the log of a start that hangs can be read.
- **Autostart:** a switch with the runlevel, `default` or `boot`. A service of `sysinit` or `shutdown` has no switch.
- **System services**, those of `sysinit` and `shutdown` (devfs, mdev, killprocs …), are hidden unless **Show system
  services** is ticked.
- **Details:** description, state with uptime and PID (when known), supervised or not, autostart, the services it needs,
  uses, starts after and is needed by (`rc-service <s> ineed`, `iuse`, `iafter`, `needsme`), and the path of its script.

The page reloads the view every 5 seconds while it is open. Without OpenRC (no `rc-service`, or no
`/run/openrc/softlevel`) it says so instead of listing services.

Everybody who may open the page sees all of it; without admin mode the extension runs as the logged-in user and shows
what that user may read (the services, their states and relations, the logs that are readable for everybody; a log
only root may read is listed with "in admin mode"). The buttons are there all the same: without admin mode a click opens
the admin dialog of edelweiss first. A user who cannot get admin mode at all sees the page with **Logs** and
**Details** only and the hint "Only administrators can change anything here."

### Actions (admin mode)

- **Start, stop, restart** with `rc-service`. The dialog names the services that stop along (those that need the
  service, `needsme`, also through others), shows the steps while they run, and if OpenRC fails, what it printed, with
  **View log**. The start of a crashed service is a restart: OpenRC's start does nothing to a service it still has as
  started.
- **Autostart on** adds the service to `default` (preselected) or `boot` with `rc-update add`; **off** takes it out of
  its runlevels with `rc-update del`. Neither starts or stops the service.
- **Restart edelweiss**: edelweiss cannot be stopped from its own page, only restarted. It keeps the sessions in memory
  only, so **the restart signs everyone out**: the page is gone for a few seconds, waits until it sees the restart and
  then reloads, which shows the login page.

### Protection classes

What the page allows depends on the class of the service: **allow**; **warn**, only after a red warning with the reason
and the box "I know what I am doing" ticked; **deny**, no button, and the API refuses (`protected`).

| Class | Services | Start | Restart | Stop | Autostart off | Autostart on |
|---|---|---|---|---|---|---|
| edelweiss | `edelweiss` | deny | allow (the restart signs everyone out) | deny | deny | deny |
| base | every service of `sysinit` and `shutdown`, and `root`, `localmount`, `fsck`, `sysfs`, `procfs`, `devfs`, `dmesg`, `mdev`, `mdevd`, `mdevd-init`, `udev`, `udev-trigger`, `udev-settle`, `modloop`, `modules`, `hwdrivers`, `hwclock`, `swclock`, `bootmisc`, `killprocs`, `mount-ro`, `savecache`, `cgroups`, `loopback`, `seedrng`, `urandom`, `machine-id` | deny | deny | deny | deny | deny |
| remote | `sshd`, `dropbear`, `tailscale`, `zerotier-one`, `openvpn`, `openvpn.*`, `wg-quick`, `wg-quick.*` | allow | warn | warn | warn | allow |
| network | `networking`, `net.*` (`net.eth0` …), `dhcpcd`, `iwd`, `wpa_supplicant`, `NetworkManager`, `networkmanager` | allow | warn | warn | warn | allow |
| firewall | `iptables`, `ip6tables`, `nftables`, `awall`, `ufw`, `firewalld` | warn | warn | warn | warn | warn |
| storage | `mdadm-raid`, `dmcrypt`, `netmount`, `edelweiss-storage`, `lvm`, `zfs-*` (`zfs-import`, `zfs-mount`, `zfs-share`, `zfs-zed`, `zfs-load-key` …) | allow | warn | warn | warn | allow |
| free | everything else (docker, nginx, samba, chronyd …) | allow | allow | allow | allow | allow |

A name matches a list exactly, except the prefixes `net.*`, `openvpn.*`, `wg-quick.*` and `zfs-*`, which match every
name that starts with them (`net.eth0`, `wg-quick.wg0`); `edelweiss` wins over every list. A stop or restart also stops
the services that need the service and are up (started, crashed, starting, or inactive: OpenRC stops those along too),
so the strictest class among them decides; with `edelweiss` or a base service among them it is denied, and the refusal
names that service. The warning of the class remote names the service it is about. Only names of the service list ever
reach a command, never the text of a request.

### Logs

**Logs** shows the last 500 lines of one source of the service; the dialog offers each source it has, in this order:

1. its `output_log` and `error_log`, set in its script or in `/etc/conf.d/<s>` (as far as the value is known without
   running the shell: quotes, `$RC_SVCNAME`, `${VAR:-default}`; a path the extension would have to guess is left out);
2. `/var/log/<s>.log`;
3. the three newest `*.log` files of `/var/log/<s>/`;
4. its lines of the system log, `/var/log/messages` of busybox `syslogd` (and the rotated `/var/log/messages.0`): those
   logged under the name of the service or of the programs of its daemons (`exec=` of `/run/openrc/daemons/<s>/`),
   leaving out programs that run all kinds of services (`sh`, `busybox`, `start-stop-daemon` …), and the lines of
   `supervise-daemon` about the service. Only the last 4 MiB are scanned, so a huge system log costs no more than that.

Only files below `/var/log`, once their links are resolved, are ever read. **Follow live** adds the new lines of the
source: it goes on from where the 500 lines ended and looks every second; a file that shrank or was replaced (rotated)
starts again from its beginning. The system log is followed in `/var/log/messages`.

### Start failed

OpenRC never marks a start that failed: the service is just stopped again, at a boot as from `rc-service`. The page
derives it: a stopped service counts as "start failed" when its last start through the page failed, or when the system
log has OpenRC's `ERROR: <s> failed to start` for it since the boot and no start or stop of it through the page has
worked since. The runner of the jobs leaves a marker per service in `/run/edelweiss-ext-openrc/failed/` and
`/run/edelweiss-ext-openrc/ok/` for that. A service that was stopped by hand after a failed start at boot stays red
until it is started or stopped through the page, or the next boot.

"Since the boot" is told in the clock of the log: the lines after the last `syslogd started: BusyBox` line, which
busybox `syslogd` writes at every start, whatever time they have. A Raspberry Pi has no real-time clock: it logs the
start of its boot with the time of the last shutdown, or of 1970, until NTP sets the clock. Only when the scanned 4 MiB
hold no such line (another syslog daemon) are they the lines after the boot time, now less the uptime of
`/proc/uptime`. The alert says when the start failed, from the failed marker or the line of the system log; a line
written before the clock was set tells no time, and the alert then has none.

### How it changes the services

Every change is planned against a fresh read of the services, checked against the protection classes, and then runs as
a **job**: a runner process of its own (`edelweiss-ext-openrc --run-job <dir>`, in a new session), so that edelweiss
stopping the extension (admin mode off, an update, the restart of edelweiss itself) never stops a command halfway. The
runner also leaves the cgroup of the OpenRC service `edelweiss` (for `/sys/fs/cgroup/edelweiss-openrc`), whose cleanup
would otherwise kill it with edelweissd. Only one change runs at a time, across all processes of the extension (the job
lock `/run/edelweiss-ext-openrc/lock`). A job lives in `/run/edelweiss-ext-openrc/jobs/<id>/` (its plan, its state and
its log); the newest 10 are kept, and a reboot clears them.

The commands run without a shell, by an absolute path (the first of `/usr/sbin`, `/usr/bin`, `/sbin`, `/bin` that has
the tool) with an argument list, with the environment `PATH=/usr/sbin:/usr/bin:/sbin:/bin`, `LC_ALL=C`,
`EINFO_COLOR=NO` only, `/` as the working directory and no standard input. Start, stop and restart may take 5 minutes
(a slow `docker` stop), every other command 30 seconds. A command that runs longer fails: it gets SIGTERM, together
with what it runs in the foreground (its process group), so that OpenRC puts a half-done start or stop back on its way
out (the service is stopped or started again, not left starting or stopping), and what is still there once the command
has ended, or 30 seconds later, is killed. Daemons that a start brought up run in a session of their own and stay.

| Purpose | Command |
|---|---|
| start, stop, restart | `rc-service <s> start`, `stop`, `restart` |
| the view | `rc-status --all --nocolor`, and the state files of `/run/openrc` |
| details, the services that stop along | `rc-service <s> ineed`, `iuse`, `iafter`, `needsme` |
| autostart on, off | `rc-update add <s> <default\|boot>`, `rc-update del <s> <runlevel>` |
| diskless and data mode | `lbu commit` (after `rc-update` only) |

### Diskless and data mode

The extension tells sys, diskless and data mode apart by itself (`/` on tmpfs is diskless, with `/var` on a disk data).
There `/etc` lives in RAM, and with it the links of the autostart in `/etc/runlevels`: a job that switched the autostart
ends with `lbu commit`, so that the change survives a reboot. Start, stop and restart change nothing below `/etc` and
need no `lbu commit`. Without `lbu` there, the autostart is not changed at all; and when the mode cannot be read, the
extension changes nothing.

## Requirements

- edelweiss 0.2.0 or newer: the build scopes the style sheet of the extension to its container, which edelweiss 0.1.0
  does not render, so there the extension would show without its styles.
- Alpine Linux with OpenRC, x86_64 or aarch64 (the container tests run on 3.24 and 3.22). Without OpenRC the page says
  so instead of listing services.

## Installation

Once the repository and its release exist (see First publication), on the edelweiss host, as root:

```sh
edelweissd ext install Pukka922/edelweiss-ext-openrc
```

or in the web interface under **Settings → Extensions → Install from GitHub** (admin mode). edelweiss shows the
release, the file and its SHA-256 together with a warning, and installs only after you confirm. Updates:
`edelweissd ext update openrc`, or the extensions page.

## Building

Requirements: Go 1.26, Node.js 22 with npm, make and a C compiler (`gcc`: `make test` runs the Go tests with the race
detector, which needs cgo). `make test-alpine` also needs Docker and network access: its containers install openrc
with apk from the Alpine mirrors, and the first run pulls the images.

```sh
make build                    # the UI, then static binaries for amd64 and arm64 in build/
make test                     # Go (race detector), vitest and the test of the release guards (scripts/)
make test-alpine              # the tests against the real OpenRC in throwaway Alpine containers (see Tests)
make lint                     # go vet, gofmt, Biome, TypeScript, actionlint
make release VERSION=0.1.0    # release files in dist/: edelweiss-ext-openrc_0.1.0_linux_<amd64|arm64>, checksums.txt
```

The Go module requires `github.com/Pukka922/edelweiss/sdk`, and `ui/package.json` names the JS SDK by its download
URL at the edelweiss release. Both come from GitHub (the Go module proxy and `npm ci`), so a fresh clone builds with
`make build test lint`.

## Development

To develop against an edelweiss checkout next to this repository (`../edelweiss`; `EDELWEISS_DIR=<path>` for another
place) instead of the published SDKs, for example for an SDK change that is not released yet:

```sh
make dev-sdk
make build test lint
```

`make dev-sdk`:

- writes a `go.work` (git-ignored) whose `replace` points the Go SDK to `../edelweiss/sdk`;
- builds the JS SDK tarball there (`make sdk-pack`);
- installs it into `ui/node_modules` with `npm install --no-save --no-package-lock`, so `ui/package.json` stays
  unchanged and no `package-lock.json` is written.

To switch back to the published SDKs, run `rm -f go.work go.work.sum && rm -rf ui/node_modules`. The next `make`
runs `npm ci`.

### Trying it in the edelweiss dev environment

The edelweiss dev environment (`make dev` in `../edelweiss`, then http://localhost:9090) is an Alpine container without
OpenRC. `dev/openrc-dev.sh` gives it one, with the test services of the container tests and three dummies (see
below). Build a release binary with a pre-release version, put it where the container sees it, start the dev
environment, set up OpenRC in it and install the extension (use `linux_arm64` on an arm64 machine):

```sh
make release VERSION=0.1.0-rc.1
mkdir -p ../edelweiss/build
cp dist/edelweiss-ext-openrc_0.1.0-rc.1_linux_amd64 ../edelweiss/build/
cd ../edelweiss
make dev
docker compose -f dev/compose.yaml exec -T edelweiss sh -s < ../edelweiss-ext-openrc/dev/openrc-dev.sh
docker compose -f dev/compose.yaml exec edelweiss /work/build/dev/edelweissd ext install \
  /work/build/edelweiss-ext-openrc_0.1.0-rc.1_linux_amd64 --config /work/dev/edelweiss.dev.toml
```

`make dev` also builds the system overview, so it needs a checkout of
[edelweiss-ext-system](https://github.com/Pukka922/edelweiss-ext-system) at `../edelweiss-ext-system` (or
`EXT_SYSTEM_DIR=<path>` set) and stops with a message if there is none. The running edelweissd picks an installed
extension up within a moment. Install again after a change: that replaces the extension and stops its processes.
`make dev` recreates the dev container, and with it OpenRC and the installed extensions: run `dev/openrc-dev.sh` and
install the file again afterwards. `dev/openrc-dev.sh` can run again at any time: it starts the services below that
should run, adds the autostart links that are missing and makes `t-crash` crash again; it stops nothing, takes nothing
out of the autostart and does not make `t-fail` fail a second time. Remove `dist/` when you are done (`rm -rf dist`).

`dev/openrc-dev.sh` refuses to run anywhere but in the edelweiss dev container: unless there is a container marker
(`/.dockerenv` or `/run/.containerenv`) and PID 1 is `edelweissd` (the entrypoint of the dev container execs it), it
says which check failed and "nothing done", and exits 1 before it changes anything. On a host it would replace `sshd`,
`networking` and `edelweiss` with dummies and lock you out at the next boot.

What `dev/openrc-dev.sh` sets up (it changes the dev container only):

- the test services of `integration/testdata/init.d`: `t-daemon`, `t-needs` (needs `t-daemon`) and `t-log` (writes
  `/var/log/t-log.log`) running, `t-crash` crashed, `t-fail` failed (its start fails, which the system log tells),
  `t-oneshot` in `boot` but stopped (the yellow state), `t-super` (supervise-daemon) stopped; not `t-hang`, whose start
  never ends: it would hold the job lock for the 5 minutes of the time limit (`make test-alpine` shows the timeout);
- the dummies `edelweiss`, `sshd` and `networking`, marked "(dev dummy)" in their description: started one-shot
  services whose start and stop do nothing, so that the page shows the classes edelweiss, remote and network without
  touching the real edelweissd of the container (which OpenRC does not run) or its network. A restart of the dummy
  edelweiss signs nobody out, so its dialog waits until its fallback reloads the page after 30 seconds;
- `edelweiss`, `networking`, `sshd`, `t-daemon` and `t-fail` in `default`, busybox `syslogd` for `/var/log/messages`
  (with a line of `t-daemon`), and `rc_cgroup_mode="none"` in `/etc/rc.conf` (the cgroups of the container are
  read-only).

The dev container has no init that reaps processes (edelweissd is its PID 1), so a daemon that ends stays a zombie, and
OpenRC counts a zombie as running. The script works around that for the daemons of the test services (a stop does not
wait for its process, `retry="0"` in `/etc/conf.d/<s>`; `t-crash` crashes by a pid file naming a process that is
gone), but not for `t-super`: a stop of a service under `supervise-daemon` waits for the supervisor to end, which it
never does there. Do not stop or restart `t-super` in the dev container; the job would hold the job lock for 5 minutes.

Files have LF line endings (`.gitattributes`, `.editorconfig`), also in a Windows checkout.

## Tests

Nothing in the tests starts, stops or reconfigures a service of the machine you work on.

- **Unit tests** (`make test`): Go with the race detector, against temporary trees of `/etc/init.d`, `/etc/runlevels`,
  `/run/openrc`, `/proc` and `/var/log` and a fake runner: the parsers against the captured outputs of the real OpenRC
  (`internal/openrc/testdata`, its README says how they were captured), every protection rule, every action as its
  plan, the log sources, tail and follow, the jobs and their runner, and the API. Then the UI tests (vitest) and the
  test of the release guards.
- **Container tests** (`make test-alpine`): the real OpenRC of Alpine in throwaway containers (below).
- **Browser test** in the edelweiss dev environment with `dev/openrc-dev.sh` (see Development): the page and its
  dialogs by hand, as a user with and without admin mode.
- **By hand on a real host** (a Raspberry Pi), what neither a container nor the dev environment has: the restart of the
  real edelweiss from its page (the runner leaving its cgroup, everyone signed out), `lbu commit` in diskless and data
  mode, real services (sshd, networking, docker) and a boot with a service whose start fails.

### Tests in throwaway containers

`make test-alpine` (`scripts/test-alpine.sh`) runs the tests of `integration/` (build tag `integration`) with a test
binary built on your machine: one `docker run --rm --init alpine:<tag>` per scenario, without extra privileges,
removed afterwards. The container installs openrc with apk (its only network access), installs the test services of
`integration/testdata/init.d`, creates `/run/openrc/softlevel` and starts busybox `syslogd`; then the test runs, as
root, or as `nobody` for the view of a user without admin mode. The commands run through the runner of the extension, so
OpenRC gets exactly the arguments and the environment the extension gives it. `TESTS=<text>` runs only the scenarios
whose name contains the text (`TESTS=Logs make test-alpine`); a filter that matches nothing is an error. A scenario
counts as passed only when its output has the `--- PASS:` line of its test. `--init` gives the container an init that
reaps processes, as on a host: without it a killed daemon stays a zombie, which OpenRC still finds.

| Scenario | What it shows |
|---|---|
| `TestCapture` | the outputs the parsers read (`rc-status`, `rc-update show`, `needsme`, `ineed`, a failed start, the system log, the files of `/run/openrc`), written to the log for `internal/openrc/testdata` |
| `TestRunlevelChange` | a failed start at a runlevel change (`openrc <runlevel>`, also sysinit, boot, default as at a boot) leaves the service stopped, never failed |
| `TestRcStatusStates` | how `rc-status` prints the states starting, inactive and failed |
| `TestTimeoutAgainstOpenRC` | `t-hang`, whose start never ends, started with a time limit of 2 seconds: the step fails, OpenRC has the service stopped (not starting), the failed marker is there, and the hang (which ignores SIGTERM) is gone |
| `TestActionsAgainstOpenRC` | the plans of the page, done by the executor of the jobs: a start (the PID of the pid file, the ok marker), a restart (a new daemon), a stop that takes `t-needs` along, autostart on and off (the link in `/etc/runlevels/default`), a failed start (the failed step has OpenRC's output, the service is stopped, the failed marker is there), a killed daemon (crashed; its start is planned as the restart that brings it back), a one-shot service |
| `TestLogsAgainstOpenRC` | the failed start of `openrc default` found in the system log as a failed start since the boot, also with a boot time after it (the start line of busybox `syslogd` decides); the `output_log` of `t-log` as a source, its last lines and a new line followed live; the lines of `t-daemon` in `/var/log/messages`, and none of another program |
| `TestReadAsUser` | as `nobody`: the view (`rc-status`, the pid files, a crashed daemon) and the details of `t-daemon` (`rc-service ineed`, `iuse`, `iafter`, `needsme`) work without root, with the states root sees |

Every scenario runs on `alpine:3.24` (OpenRC 0.63) and `alpine:3.22` (OpenRC 0.62). The tests refuse to run unless
`EDELWEISS_ALPINE_TEST` is set (`1` as root, `user` for the test as `nobody`) and `/.dockerenv` and
`/etc/alpine-release` exist. Anywhere else (`go test -tags integration ./integration` on your machine) they print a
note and pass without doing anything. For the tests, Docker is used for exactly one thing: the throwaway containers of
`make test-alpine`.

## Releasing

No file needs changing for a release: the git tag is the only source of the version.

1. Check the version with `make release-check VERSION=<version>`. It has to be a plain `x.y.z` or `x.y.z-pre` by the
   rules of Go (no leading zeros, no empty pre-release identifier, no `+build` metadata).
2. Push `main`, then the tag, which decides the version: `git push origin main`, then
   `git tag v<version> && git push origin v<version>`.

`manifest.json` holds the placeholder version `0.0.0-dev`. The release build puts the version of the tag into the
binaries (`-ldflags -X main.version`) and checks it by running the binary for the build machine with
`--edelweiss-manifest`, because edelweiss refuses a binary whose manifest version differs from the version in the name
of the release file. A local `make build` without `VERSION` reports `0.0.0-dev`.

The release workflow (`.github/workflows/release.yml`) has two jobs, so that the build cannot use the write token.
`build` has a read-only token and keeps no credentials in the checkout. It checks the tag name (`v` and then only
letters, digits, `.`, `+` and `-`), then runs three make targets and hands `dist/` on as a workflow artifact:

- `make release-check`: the tag is a plain `x.y.z` or `x.y.z-pre`;
- `make lint test`: the checks of the CI workflow, so a tag on a commit that fails them publishes nothing;
- `make release`: the release files in `dist/`, with the version of the tag in the binaries.

`publish` runs when `build` has succeeded. It holds the write token (gh needs it), builds nothing, needs neither Go nor
Node and keeps no credentials in its checkout either; only the `gh-release` step gets the token, through `GH_TOKEN`. It
downloads the artifact into `dist/` and runs `make gh-release`: it checks that `dist/` holds exactly `checksums.txt` and
the two binaries of this version, that the files match `checksums.txt` and that it lists exactly them, and creates the
GitHub release `v<version>` with `edelweiss-ext-openrc_<version>_linux_amd64`,
`edelweiss-ext-openrc_<version>_linux_arm64` and `checksums.txt`. A version with a `-` (`0.1.0-rc.1`) is published
as a GitHub pre-release: the installer takes the latest release, which leaves pre-releases out, so an rc is not offered
as an update.

A compromised build could still hand over altered files under the expected names: `checksums.txt` shows that the files
are consistent, not where they come from.

**Published versions are immutable.** Once a release may have been fetched, never move or delete the tag `v<version>`
and never replace or delete the files of the release:

- edelweiss shows the SHA-256 of the file to whoever installs the extension and refuses a release that changed after
  that (`release_changed`);
- sum.golang.org records every version of this Go module that has been fetched, so a moved tag makes `go get` fail with
  "checksum mismatch" for everyone, new fetchers included, not only for those who fetched the old one.

A broken release is fixed with a new patch version.

Rerunning a failed workflow is fine as long as the tag stays on the same commit. "Re-run failed jobs" repeats only the
job that failed, and "Re-run all jobs" builds again (the upload of the artifact replaces the one of the first attempt).
`gh release create` fails if the release already exists, so if the workflow had already created the GitHub release,
delete only the release (never the tag) before the rerun. Do that only for a release that nobody can have downloaded
yet; otherwise publish a new patch version.

The CI workflow (`.github/workflows/ci.yml`) runs `make lint test build` on every push and pull request.

## First publication

edelweiss 0.2.0 is published, and this repository uses its SDKs: `go.sum` and `ui/package-lock.json` pin them,
so `go get`, `go mod tidy` and `npm ci` work directly and the workflows build without an edelweiss checkout. What is
left is the repository on GitHub:

1. Create the public repository `Pukka922/edelweiss-ext-openrc` on GitHub. Recommended before the first tag: a tag
   ruleset (Settings → Rules → Rulesets, target: tags) that blocks deleting and updating the tags `v*`, so that a
   published version cannot be moved by mistake (see Releasing above). Then push `main` and the tag:

   ```sh
   git remote add origin https://github.com/Pukka922/edelweiss-ext-openrc.git
   git push -u origin main
   git tag v0.1.0
   git push origin v0.1.0
   ```

   When the release workflow has finished, `edelweissd ext install Pukka922/edelweiss-ext-openrc` installs the
   extension, as does **Settings → Extensions → Install from GitHub** in the web interface (admin mode).

## License

MIT, Copyright (c) 2026 Pukka922, see LICENSE.
