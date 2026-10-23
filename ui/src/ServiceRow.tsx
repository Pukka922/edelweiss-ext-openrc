import { useI18n } from "@edelweiss/sdk";
import {
  IconFileText,
  IconInfoCircle,
  IconPlayerPlay,
  IconPlayerStop,
  IconRefresh,
} from "@tabler/icons-solidjs";
import { type Component, createUniqueId, Show } from "solid-js";
import { autostartLevels, type Class, isSystem, type Kind, type ServiceView } from "./api";
import { stateText } from "./errors";
import { IconButton, type IconProps } from "./IconButton";

/** What a service row asks the page for: the dialog of kind for the service. */
export type OnAction = (kind: Kind, service: ServiceView) => void;

/** The kinds that have an icon button. */
export type ButtonKind = "logs" | "restart" | "stop" | "start" | "details";

const ICONS: Record<ButtonKind, Component<IconProps>> = {
  logs: IconFileText,
  restart: IconRefresh,
  stop: IconPlayerStop,
  start: IconPlayerPlay,
  details: IconInfoCircle,
};

/**
 * An icon button for kind on service: named "<text> <service>" (for example "Restart nginx"), with the text as its
 * tooltip. The stop button is red.
 */
export function ActionButton(props: { kind: ButtonKind; service: ServiceView; onAction: OnAction }) {
  const { t } = useI18n();
  return (
    <IconButton
      icon={ICONS[props.kind]}
      label={`${t(`actions.${props.kind}`)} ${props.service.name}`}
      tooltip={t(`actions.${props.kind}`)}
      danger={props.kind === "stop"}
      onClick={() => props.onAction(props.kind, props.service)}
    />
  );
}

/** The spinner that takes the place of the buttons that change the service while a change of it runs. */
export function Spinner() {
  const { t } = useI18n();
  return <span class="loading loading-spinner loading-sm mx-2" role="status" aria-label={t("row.working")} />;
}

/** The classes that are marked in the list, and the colour of their badge. */
const BADGES: Partial<Record<Class, string>> = {
  edelweiss: "badge-info",
  remote: "badge-warning",
  network: "badge-warning",
  firewall: "badge-warning",
  storage: "badge-warning",
};

/** The colour of the dot before the status: green runs, red is a problem, yellow is on its way or does not run. */
function dotClass(s: Pick<ServiceView, "state" | "autostartIdle">): string {
  switch (s.state) {
    case "started":
      return "bg-success";
    case "crashed":
    case "failed":
      return "bg-error";
    case "starting":
    case "stopping":
    case "inactive":
      return "bg-warning";
    default:
      return s.autostartIdle ? "bg-warning" : "bg-base-content/30";
  }
}

/** The colour of the status text: a problem is red, a stopped service of the autostart yellow. */
function statusClass(s: Pick<ServiceView, "state" | "autostartIdle">): string {
  if (s.state === "crashed" || s.state === "failed") return "text-error";
  return s.state === "stopped" && s.autostartIdle ? "text-warning" : "";
}

/**
 * A row of the table: the name with its badge, the description (and that the service is supervised), the status with a
 * dot, the autostart as a switch with its runlevel, and the icon buttons. A button shows only when its verdict (allowed)
 * is not deny, and the state has the action: a service that is up (running, inactive or crashed) is restarted or stopped,
 * one that is down (stopped or failed) is started. While the service starts or stops, or busy says that a job for it
 * runs, a spinner takes the place of the buttons that change it, between Logs and Details: the log is what tells why a
 * start hangs. A page that is readOnly (a user who cannot get admin mode) has no button that changes anything and a
 * switch that only shows; logs and details are for everybody. A service of sysinit or shutdown has no switch. Reads
 * service when it needs it, so that a new view every few seconds keeps the row, its buttons and their focus.
 */
export function ServiceRow(props: {
  service: ServiceView;
  readOnly: boolean;
  /** A job for the service runs. */
  busy?: boolean;
  onAction: OnAction;
}) {
  const { t } = useI18n();
  const s = () => props.service;
  const badge = () => BADGES[s().class];
  const description = () =>
    [s().description, s().supervised ? t("row.supervised") : ""].filter(Boolean).join(" · ");
  const levels = () => autostartLevels(s());
  const on = () => levels().length > 0;
  const locked = () => s().allowed[on() ? "disable" : "enable"] === "deny";
  // why the switch cannot be used: only for a verdict of deny, and not for a page that has no change at all
  const lockReason = () => (locked() && !props.readOnly ? t(on() ? "row.noLeave" : "row.noJoin") : undefined);
  const switchTitle = () => {
    if (props.readOnly) return undefined;
    return lockReason() ?? t(on() ? "row.turnOff" : "row.turnOn");
  };
  const reasonId = createUniqueId();
  // a service that is up is restarted or stopped (a crashed one too: it is still started for OpenRC); one that is down is
  // started
  const stoppable = () => s().state === "started" || s().state === "inactive" || s().state === "crashed";
  const startable = () => s().state === "stopped" || s().state === "failed";
  const working = () => props.busy || s().state === "starting" || s().state === "stopping";
  const may = (kind: "start" | "stop" | "restart") => !props.readOnly && s().allowed[kind] !== "deny";
  const button = (kind: ButtonKind) => <ActionButton kind={kind} service={s()} onAction={props.onAction} />;
  return (
    <tr>
      <td>
        <div class="flex flex-wrap items-center gap-x-2">
          <span class="font-semibold">{s().name}</span>
          <Show when={badge()}>
            {(cls) => <span class={`badge badge-sm badge-soft ${cls()}`}>{t(`classes.${s().class}`)}</span>}
          </Show>
        </div>
        <Show when={description()}>
          <div class="text-xs opacity-70">{description()}</div>
        </Show>
      </td>
      <td>
        <span class="flex items-center gap-2">
          <span class={`inline-block size-2 shrink-0 rounded-full ${dotClass(s())}`} aria-hidden="true" />
          <span class={statusClass(s())}>{stateText(t, s())}</span>
        </span>
      </td>
      <td>
        <Show
          when={!isSystem(s())}
          fallback={<span class="text-sm opacity-70">{s().runlevels.join(", ")}</span>}
        >
          {/* the title is on the wrapper: a disabled control shows no tooltip in some browsers */}
          <span class="flex flex-nowrap items-center gap-2" title={switchTitle()}>
            <input
              type="checkbox"
              role="switch"
              class="toggle toggle-sm toggle-primary"
              checked={on()}
              aria-checked={on()}
              disabled={props.readOnly || locked()}
              aria-label={`${t("row.autostart")} ${s().name}`}
              aria-describedby={lockReason() ? reasonId : undefined}
              // the switch shows what the view says; a change goes through the dialog, and the next view moves it
              onClick={(e) => {
                e.preventDefault();
                props.onAction(on() ? "disable" : "enable", s());
              }}
            />
            <span class="text-sm" classList={{ "opacity-70": !on() }}>
              {on() ? levels().join(", ") : t("row.autostartOff")}
            </span>
            <Show when={lockReason()}>
              {(reason) => (
                <span id={reasonId} class="sr-only">
                  {reason()}
                </span>
              )}
            </Show>
          </span>
        </Show>
      </td>
      <td class="w-px whitespace-nowrap text-right">
        <div class="flex items-center justify-end">
          {button("logs")}
          <Show when={!working()} fallback={<Spinner />}>
            <Show when={stoppable() && may("restart")}>{button("restart")}</Show>
            <Show when={stoppable() && may("stop")}>{button("stop")}</Show>
            <Show when={startable() && may("start")}>{button("start")}</Show>
          </Show>
          {button("details")}
        </div>
      </td>
    </tr>
  );
}
