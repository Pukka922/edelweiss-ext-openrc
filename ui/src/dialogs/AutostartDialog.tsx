import { useI18n } from "@edelweiss/sdk";
import { createSignal, For, Show } from "solid-js";
import { AUTOSTART_LEVELS, autostartLevels, fits } from "../api";
import { tSafe } from "../errors";
import { Field, JobStatus, Modal } from "../Modal";
import { ChangeButtons, Changed, type ChangeProps, useChange, WarningNotice } from "./change";

/** The kinds this dialog is for. */
export type AutostartKind = "enable" | "disable";

/** The runlevel that is preselected when the autostart is switched on: nearly every service belongs in it. */
const DEFAULT_LEVEL = "default";

/**
 * The autostart of a service. Switching it on offers the runlevel (default, preselected, or boot). Switching it off asks
 * for a confirmation that names the runlevels it takes the service out of: it sends the runlevel when the service is in
 * only one of default and boot, and none when it is in both (the server then leaves both). Neither starts or stops the
 * service, which the dialog says. A class with a warning gets the red callout and the check "I know what I am doing", as
 * in ActionDialog, and a job that fails shows the output of the failed command with the way to the log. A change that does
 * not fit the service as the view has it (switching on what is in default or boot already) is not sent: the dialog says
 * that the service changed.
 */
export function AutostartDialog(props: ChangeProps & { kind: AutostartKind }) {
  const { t } = useI18n();
  const at = props.service;
  const name = at.name;
  const kind = props.kind;
  const title = () => tSafe(t, "autostart.title", { name });
  if (!fits(kind, at)) return <Changed title={title()} onClose={props.onClose} />;
  const change = useChange(props, kind);
  const levels = autostartLevels(at);
  const [level, setLevel] = createSignal(DEFAULT_LEVEL);
  const runlevel = () => (kind === "enable" ? level() : levels.length === 1 ? levels[0] : undefined);
  return (
    <Modal
      title={change.hasOutput() ? tSafe(t, "autostart.failed", { name }) : title()}
      busy={change.busy()}
      onClose={props.onClose}
      actions={
        <ChangeButtons
          change={change}
          label={t("autostart.apply")}
          onClose={props.onClose}
          onLogs={props.onLogs}
          onConfirm={() => change.submit(runlevel())}
        />
      }
    >
      <Show
        when={kind === "enable"}
        fallback={<p>{tSafe(t, "autostart.textOff", { name, levels: levels.join(", ") })}</p>}
      >
        <Field label={t("autostart.runlevel")}>
          {(id) => (
            <select
              id={id}
              class="select select-sm w-full"
              disabled={change.busy() || change.over()}
              onChange={(e) => setLevel(e.currentTarget.value)}
            >
              <For each={AUTOSTART_LEVELS}>
                {(l) => (
                  <option value={l} selected={l === level()}>
                    {t(`autostart.level.${l}`)}
                  </option>
                )}
              </For>
            </select>
          )}
        </Field>
      </Show>
      <p class="text-xs opacity-70">{t(kind === "enable" ? "autostart.noteOn" : "autostart.noteOff")}</p>
      <Show when={!change.over()}>
        <WarningNotice change={change} name={name} />
      </Show>
      <JobStatus step={change.step()} failure={change.failure()} />
    </Modal>
  );
}
