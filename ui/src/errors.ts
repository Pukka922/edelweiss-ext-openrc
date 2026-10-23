import { ApiError, type Params, type Translator } from "@edelweiss/sdk";
import type { JobState, ServiceView } from "./api";

const MARK = "\u0000";

/**
 * t(key, params) with values that stay what they are. The t() of the SDK puts a value in with String.replace(pattern,
 * string), which reads "$'", "$&", "$`" and "$$" in the value as replacement patterns: "mount: unknown device 'a$' in
 * fstab" would be garbled, with the text after the placeholder pasted in at the "$'". So t gets sentinels (strings
 * without a "$"), and the real values take their place afterwards, in one pass over the text (a value is never read
 * again as a placeholder or a sentinel). Use it for every text that carries a name or a message of the server: they
 * may hold a "$".
 */
export function tSafe(t: Translator["t"], key: string, params: Params = {}): string {
  const names = Object.keys(params);
  const sentinels = Object.fromEntries(names.map((name, i) => [name, `${MARK}${i}${MARK}`]));
  // split at the marks: the odd parts are the numbers of the sentinels, the even ones the text between them
  return t(key, sentinels)
    .split(MARK)
    .map((part, i) => {
      const name = i % 2 === 1 ? names[Number(part)] : undefined;
      return name === undefined ? part : String(params[name]);
    })
    .join("");
}

/** The text of key with params, or undefined when there is none. */
function textOf(t: Translator["t"], key: string, params: Params = {}): string | undefined {
  const text = tSafe(t, key, params);
  return text === key ? undefined : text;
}

const isText = (s: unknown): s is string => typeof s === "string" && s !== "";

/**
 * The fields of an invalid_input that tell the service is not as the request took it: already started or stopped,
 * already in the runlevel, not in it, in neither (internal/actions settle). The page sends no kind and no runlevel the
 * server does not know, so each of them means that the service changed since the page read it: one text for all.
 */
const CHANGED_FIELDS = ["kind", "runlevel"];

/**
 * A text for an error of the backend: the translation of its code (errors.<code>, with the message of the server); for
 * invalid_input the one of its field (errors.field.<field>: the service changed meanwhile, or there is no such service),
 * for protected the service it names and its class. A code, field or refusal without a text gives the message of the
 * server.
 */
export function errorText(err: unknown, t: Translator["t"]): string {
  if (!(err instanceof ApiError)) return err instanceof Error ? err.message : String(err);
  const { field, reason, service } = err.details;
  switch (err.code) {
    case "invalid_input": {
      if (!isText(field)) break;
      const text = textOf(t, `errors.field.${CHANGED_FIELDS.includes(field) ? "changed" : field}`);
      if (text !== undefined) return text;
      break;
    }
    case "protected": {
      if (!isText(service)) return err.message;
      const cls = isText(reason) ? textOf(t, `classes.${reason}`) : undefined;
      return cls === undefined
        ? tSafe(t, "errors.protectedNoReason", { service })
        : tSafe(t, "errors.protected", { service, reason: cls });
    }
  }
  return textOf(t, `errors.${err.code}`, { message: err.message }) ?? err.message;
}

/**
 * Whether an error is a refusal that says the page read the service before it changed: an invalid input (the kind or the
 * runlevel does not fit it any more, or there is no such service) or a service that is not found. The page then reads the
 * view again.
 */
export const staleRefusal = (err: unknown): boolean =>
  err instanceof ApiError && (err.code === "invalid_input" || err.code === "not_found");

/**
 * The red warning of a change for the protection class named reason (warn.<reason>: remote, network, firewall,
 * storage): what the change costs. A class without a text of its own gives the plain demand for a confirmation.
 */
export function warnText(t: Translator["t"], reason: string, params: Params = {}): string {
  return textOf(t, `warn.${reason}`, params) ?? t("errors.confirm_required");
}

/**
 * How long ago the time iso was, seen at now (milliseconds): "vor 4 min" as the preview has it, in whole minutes below
 * an hour, in hours below a day, then in days; below a minute, and for a time after now (the clock of the browser is
 * behind the one of the host), just now. Undefined for a time that cannot be read.
 */
export function agoText(t: Translator["t"], iso: string, now: number): string | undefined {
  const at = Date.parse(iso);
  if (Number.isNaN(at)) return undefined;
  const minutes = Math.floor(Math.max(0, now - at) / 60_000);
  if (minutes < 1) return t("ago.now");
  if (minutes < 60) return tSafe(t, "ago.minutes", { n: minutes });
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return tSafe(t, "ago.hours", { n: hours });
  const days = Math.floor(hours / 24);
  return days === 1 ? t("ago.daysOne") : tSafe(t, "ago.days", { n: days });
}

/** The text of the label of a step (steps.<label>), the label itself when it has none. */
export function stepLabel(t: Translator["t"], label: string | undefined): string {
  return label === undefined ? "" : (textOf(t, `steps.${label}`) ?? label);
}

/** The step a job is at: "Step 1/2: restart the service". */
export function stepText(st: JobState, t: Translator["t"]): string {
  return tSafe(t, "job.step", { n: st.step + 1, m: st.steps.length, label: stepLabel(t, st.steps[st.step]) });
}

/**
 * The text of a job that failed or was interrupted: jobs.<code> with the step and the message of the server (failed,
 * interrupted); jobs.failed for a code without a text. The output of the failed command is not in it: the dialog shows
 * it apart.
 */
export function jobErrorText(st: JobState, t: Translator["t"]): string {
  const params = {
    n: st.step + 1,
    m: st.steps.length,
    label: stepLabel(t, st.steps[st.step]),
    error: st.error ?? st.code ?? "",
  };
  return textOf(t, `jobs.${st.code ?? "failed"}`, params) ?? tSafe(t, "jobs.failed", params);
}

/**
 * What a service is doing, in words: running (started), started once (a started service without a daemon), stopped,
 * crashed, start failed, starting, stopping, inactive; a stopped service of the autostart is "in the autostart, not
 * running". A state without a text is shown as it is.
 */
export function stateText(
  t: Translator["t"],
  s: Pick<ServiceView, "state" | "oneShot" | "autostartIdle">,
): string {
  if (s.state === "started" && s.oneShot) return t("state.oneShot");
  if (s.state === "stopped" && s.autostartIdle) return t("state.autostartIdle");
  return textOf(t, `state.${s.state}`) ?? s.state;
}
