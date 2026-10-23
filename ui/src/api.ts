/**
 * The shapes of the extension's API (internal/api, internal/openrc, internal/policy, internal/jobs, internal/actions)
 * and the rules of the server that the page needs to show what it shows.
 */

/** The state of a service in the view (openrc.State). A stopped service whose start failed is failed. */
export type State = "started" | "stopped" | "crashed" | "failed" | "starting" | "stopping" | "inactive";

/** The protection class of a service (policy.Class). */
export type Class = "edelweiss" | "base" | "remote" | "network" | "firewall" | "storage" | "free";

/** What can be done to a service (policy.Action): the kinds of a change. enable and disable switch the autostart. */
export type Action = "start" | "stop" | "restart" | "enable" | "disable";

/** What the policy says to an action (policy.Verdict): warn is only for the red warning with a confirmation. */
export type Verdict = "allow" | "warn" | "deny";

/** What a click on a service asks the page for: the dialog of a change, the logs or the details. */
export type Kind = Action | "logs" | "details";

/** A service of /etc/init.d with its state (openrc.Service). */
export interface Service {
  name: string;
  description: string;
  state: State;
  /** Started without a tracked daemon. */
  oneShot: boolean;
  /** supervise-daemon restarts it after a crash. */
  supervised: boolean;
  /** The runlevels it is added to. */
  runlevels: string[];
  pid?: number;
  /** As rc-status prints it, when it does. */
  uptime?: string;
  /** /etc/init.d/<name> */
  script: string;
}

/** A service as the page shows it (api.ServiceView). */
export interface ServiceView extends Service {
  class: Class;
  /** What the policy says to each action on the service alone. */
  allowed: Record<Action, Verdict>;
  /** In the autostart (default or boot) but stopped, neither crashed nor failed. */
  autostartIdle: boolean;
  /** When the start of a failed service failed (RFC 3339), when the server knows it. */
  failedAt?: string;
}

/** The numbers of the head (api.Counts): all services, those that run, the problems (crashed or failed). */
export interface Counts {
  total: number;
  running: number;
  problems: number;
}

export type JobStatus = "running" | "done" | "failed" | "interrupted";

/** A job of the extension (jobs.State). */
export interface JobState {
  id: string;
  kind: string;
  /** The name of the service. */
  target: string;
  status: JobStatus;
  /** The labels of the steps: steps.<label> is their text. */
  steps: string[];
  /** The running or failed step, from 0. */
  step: number;
  code?: string;
  error?: string;
  /** The end of the output of the failed command. */
  output?: string;
  started: string;
  finished?: string;
}

/** GET /view (api.ViewResponse): without OpenRC there are no services. The job is left out when there is none. */
export interface ViewResponse {
  present: boolean;
  /** sys, diskless, data; "" when it cannot be read */
  mode: string;
  services: ServiceView[];
  counts: Counts;
  /** The newest job while it is running or interrupted. */
  job?: JobState;
}

/** GET /services/<name> (api.Details): the service as the view shows it, and its relations, each list sorted. */
export interface Details extends ServiceView {
  /** ineed */
  needs: string[];
  /** iuse */
  uses: string[];
  /** iafter */
  after: string[];
  /** needsme */
  neededBy: string[];
}

/** POST /jobs (actions.Request). runlevel is for enable and disable; confirm is the red warning, confirmed. */
export interface JobRequest {
  kind: Action;
  service: string;
  runlevel?: string;
  confirm?: boolean;
}

/** A log of a service (logs.Source): id is "output", "error", "file:<path below /var/log>" or "syslog". */
export interface LogSource {
  id: string;
  /** The label the backend words (German for every user): the page labels a source by its id instead. */
  label: string;
  /** The file it reads: /var/log/... */
  path: string;
  /** Whether the process of the extension may read it (root may read more than a user). */
  readable: boolean;
}

/**
 * GET /services/<name>/logs (api.LogsResponse): the sources of the service, the one shown (source, "" when none can be
 * read) with its last lines, and the byte offset to follow it from.
 */
export interface LogsResponse {
  sources: LogSource[];
  source: string;
  lines: string[];
  offset: number;
  capped: boolean;
}

/** The runlevels the autostart is about. */
export const AUTOSTART_LEVELS = ["default", "boot"];

/** The runlevels of the autostart the service is in: default and boot. */
export const autostartLevels = (s: Pick<Service, "runlevels">): string[] =>
  s.runlevels.filter((l) => AUTOSTART_LEVELS.includes(l));

/** Whether the service is in the autostart (api.inAutostart). */
export const inAutostart = (s: Pick<Service, "runlevels">): boolean => autostartLevels(s).length > 0;

/** Whether the service is a system service: it is in sysinit or shutdown. These are of the class base. */
export const isSystem = (s: Pick<Service, "runlevels">): boolean =>
  s.runlevels.some((l) => l === "sysinit" || l === "shutdown");

/**
 * Whether a service in this state goes down with what it needs (policy.goesDown): OpenRC stops the started ones (a
 * one-shot service too; crashed, which OpenRC keeps in its started state) and the inactive ones; starting counts as
 * well. A stopped, stopping or failed service stops nothing.
 */
export const goesDown = (s: Pick<Service, "state">): boolean =>
  s.state === "started" || s.state === "starting" || s.state === "crashed" || s.state === "inactive";

/**
 * Whether the change still makes sense for the service as the view has it, as internal/actions settle judges it: a start
 * of a service that is not started or starting (the start of a crashed one is a restart), a stop of one that is not
 * stopped or stopping (failed is a stopped service whose start failed), a restart of any, enable for a service that is in
 * neither default nor boot and disable for one that is in either. A dialog of a change that does not fit sends nothing.
 */
export function fits(kind: Action, s: ServiceView): boolean {
  switch (kind) {
    case "start":
      return s.state !== "started" && s.state !== "starting";
    case "stop":
      return s.state !== "stopped" && s.state !== "stopping" && s.state !== "failed";
    case "enable":
      return !inAutostart(s);
    case "disable":
      return inAutostart(s);
    default:
      return true;
  }
}

/** Whether the service is down with a problem: it crashed, or its start failed. */
export const hasProblem = (s: Pick<Service, "state">): boolean =>
  s.state === "crashed" || s.state === "failed";

/** The kinds that change a service or its autostart; they need admin mode. The rest only reads. */
export const CHANGES: readonly Kind[] = ["start", "stop", "restart", "enable", "disable"];

/** The URL of a service below the API. */
export const servicePath = (name: string): string => `/services/${encodeURIComponent(name)}`;
