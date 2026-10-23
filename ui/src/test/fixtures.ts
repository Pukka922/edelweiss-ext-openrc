import type { Action, Class, Counts, Details, JobState, ServiceView, Verdict, ViewResponse } from "../api";

/** What the policy says to each action on a service of each class (internal/policy, the matrix). */
const MATRIX: Record<Class, Record<Action, Verdict>> = {
  edelweiss: { start: "deny", restart: "allow", stop: "deny", disable: "deny", enable: "deny" },
  base: { start: "deny", restart: "deny", stop: "deny", disable: "deny", enable: "deny" },
  remote: { start: "allow", restart: "warn", stop: "warn", disable: "warn", enable: "allow" },
  network: { start: "allow", restart: "warn", stop: "warn", disable: "warn", enable: "allow" },
  firewall: { start: "warn", restart: "warn", stop: "warn", disable: "warn", enable: "warn" },
  storage: { start: "allow", restart: "warn", stop: "warn", disable: "warn", enable: "allow" },
  free: { start: "allow", restart: "allow", stop: "allow", disable: "allow", enable: "allow" },
};

/**
 * A service as GET /view has it (api.ServiceView): started, in default, of the class free, unless over says otherwise.
 * allowed follows the class and the runlevels as policy.Allowed does (disabling a service that is in no runlevel is
 * denied), unless over has its own.
 */
export const service = (over: Partial<ServiceView> & { name: string }): ServiceView => {
  const s: ServiceView = {
    description: "",
    state: "started",
    oneShot: false,
    supervised: false,
    runlevels: ["default"],
    script: `/etc/init.d/${over.name}`,
    class: "free",
    allowed: { ...MATRIX.free },
    autostartIdle: false,
    ...over,
  };
  if (!over.allowed) {
    s.allowed = { ...MATRIX[s.class] };
    if (s.runlevels.length === 0) s.allowed.disable = "deny";
  }
  return s;
};

/** The numbers of the head, counted as api.count does. */
export const counts = (list: ServiceView[]): Counts => ({
  total: list.length,
  running: list.filter((s) => s.state === "started").length,
  problems: list.filter((s) => s.state === "crashed" || s.state === "failed").length,
});

/**
 * The host of the approved preview as the view has it, in the order of the server (by name): edelweiss and sshd
 * (protected), docker (supervised), nginx (crashed), redis (its start failed), chronyd, samba (stopped, in no runlevel),
 * cups (in the autostart, not running), networking and mdadm-raid (started, no daemon), net.eth0 (inactive), cronie
 * (starting), rsyslog (stopping), and the system services devfs and killprocs. 15 services, 8 of them running, 2
 * problems.
 */
export const services = (): ServiceView[] => [
  service({ name: "chronyd", description: "NTP-Zeitdienst" }),
  service({ name: "cronie", description: "Zeitgesteuerte Aufgaben", state: "starting" }),
  service({ name: "cups", description: "CUPS printing", state: "stopped", autostartIdle: true }),
  service({
    name: "devfs",
    description: "Populate /dev",
    oneShot: true,
    class: "base",
    runlevels: ["sysinit"],
  }),
  service({ name: "docker", description: "Docker Daemon", supervised: true, pid: 2481, uptime: "03:12:09" }),
  service({ name: "edelweiss", description: "edelweiss web admin", class: "edelweiss", pid: 412 }),
  service({
    name: "killprocs",
    description: "Kill all processes",
    oneShot: true,
    class: "base",
    runlevels: ["shutdown"],
  }),
  service({
    name: "mdadm-raid",
    description: "RAID-Verbünde zusammensetzen",
    oneShot: true,
    class: "storage",
    runlevels: ["boot"],
  }),
  service({
    name: "net.eth0",
    description: "Ethernet",
    state: "inactive",
    class: "network",
    runlevels: ["boot"],
  }),
  service({
    name: "networking",
    description: "Netzwerkschnittstellen",
    oneShot: true,
    class: "network",
    runlevels: ["boot"],
  }),
  service({ name: "nginx", description: "nginx web server", state: "crashed" }),
  service({ name: "redis", description: "Redis", state: "failed" }),
  service({ name: "rsyslog", description: "Syslog", state: "stopping" }),
  service({ name: "samba", description: "SMB-Dateifreigaben", state: "stopped", runlevels: [] }),
  service({ name: "sshd", description: "OpenBSD Secure Shell server", class: "remote", pid: 880 }),
];

/**
 * The view of the host of the preview. In admin mode the process of the extension runs as root and may read more; a
 * test that needs a different view then makes it itself.
 */
export const view = (over: Partial<ViewResponse> = {}): ViewResponse => {
  const list = services();
  return { present: true, mode: "sys", services: list, counts: counts(list), ...over };
};

/** The view of a host without OpenRC: no services (api.ViewResponse with present false). */
export const noOpenRC = (): ViewResponse => ({
  present: false,
  mode: "",
  services: [],
  counts: { total: 0, running: 0, problems: 0 },
});

/** A service of the view by its name. */
export const find = (v: ViewResponse, name: string): ServiceView => v.services.find((s) => s.name === name)!;

/** GET /services/<name> (api.Details): the service of the view and its relations. */
export const details = (name: string, over: Partial<Details> = {}): Details => ({
  ...find(view(), name),
  needs: [],
  uses: [],
  after: [],
  neededBy: [],
  ...over,
});

/** A job: running at its first step unless over says otherwise. */
export const job = (over: Partial<JobState> = {}): JobState => ({
  id: "20261020T120000-ab12",
  kind: "restart",
  target: "nginx",
  status: "running",
  steps: ["restart", "lbu"],
  step: 0,
  started: "2026-10-20T12:00:00Z",
  ...over,
});

/** The services of the preview's host that the tests of the dialogs name, as the view has them. */
export const docker = () => find(view(), "docker");
export const sshd = () => find(view(), "sshd");
export const samba = () => find(view(), "samba");

/** A firewall that is stopped and in no runlevel: every change of it asks for the red warning. */
export const nftables = () =>
  service({ name: "nftables", class: "firewall", state: "stopped", runlevels: [] });
