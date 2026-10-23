import { useApi, useI18n } from "@edelweiss/sdk";
import { createMemo, createResource, Show } from "solid-js";
import { type Details, fits, goesDown, type ServiceView, servicePath } from "../api";
import { tSafe } from "../errors";
import { JobStatus, Modal } from "../Modal";
import { ChangeButtons, Changed, type ChangeProps, useChange, WarningNotice } from "./change";

/** The kinds this dialog is for; the autostart has its own. */
export type ActionKind = "start" | "stop" | "restart";

/**
 * Starts, stops or restarts a service after a confirmation. A stop or restart names the services that stop along: those
 * that need the service (its relations, read when the dialog opens) and are up in the view the dialog opened with; where
 * the server asks for the confirmation after the request, the services it names take their place. A change the policy
 * warns about shows the red callout of its class and the check "I know what I am doing", and the red button sends
 * confirm: true; the callout names the service it is about, the one of the class of the warning: the service itself, or
 * else the first that stops along with that class. A job that fails shows the output of OpenRC under the title of the
 * failure (without the text of the change), with the way to the log. A change that does not fit the service as the
 * view has it (a start of a started one, a stop of a stopped one) is not sent: the dialog says that the service
 * changed. When the relations cannot be read (one of the lookups of OpenRC failed) the dialog says that the services
 * that stop along are not known, and the change goes on: the server plans it with its own reading and decides.
 */
export function ActionDialog(props: ChangeProps & { kind: ActionKind; services: ServiceView[] }) {
  const { t } = useI18n();
  const at = props.service;
  const name = at.name;
  const kind = props.kind;
  const title = (key: "title" | "failed") => tSafe(t, `action.${key}.${kind}`, { name });
  if (!fits(kind, at)) return <Changed title={title("title")} onClose={props.onClose} />;
  const api = useApi();
  const change = useChange(props, kind);
  // a start stops nothing along; for a stop or restart the dialog waits for the answer (it may be an error)
  const [relations] = createResource(
    () => kind !== "start",
    () => api.get<Details>(servicePath(name)),
  );
  const ready = () => kind === "start" || relations.state === "ready" || relations.state === "errored";
  const up = new Set(props.services.filter(goesDown).map((s) => s.name));
  const along = createMemo(
    () =>
      change.warning()?.services ??
      (relations.error ? [] : (relations.latest?.neededBy ?? []).filter((n) => up.has(n))),
  );
  // the server decides among the classes that warn by the target first, then by the name of what stops along
  const classOf = new Map(props.services.map((s) => [s.name, s.class]));
  const warned = () => {
    const reason = change.warning()?.reason;
    if (reason === undefined || at.class === reason) return name;
    return along().find((n) => classOf.get(n) === reason) ?? name;
  };
  return (
    <Modal
      title={change.hasOutput() ? title("failed") : title("title")}
      busy={change.busy()}
      onClose={props.onClose}
      actions={
        <ChangeButtons
          change={change}
          label={t(`actions.${kind}`)}
          disabled={!ready()}
          onClose={props.onClose}
          onLogs={props.onLogs}
          onConfirm={() => change.submit()}
        />
      }
    >
      <Show when={!change.hasOutput()}>
        <p>{tSafe(t, `action.text.${kind}`, { name })}</p>
      </Show>
      <Show when={relations.error && change.warning()?.services === undefined && !change.over()}>
        <div class="alert alert-warning text-sm" role="alert">
          {t("action.alongUnknown")}
        </div>
      </Show>
      <Show when={along().length > 0}>
        <p class="text-sm">{tSafe(t, "action.along", { names: along().join(", ") })}</p>
      </Show>
      <Show when={!change.over()}>
        <WarningNotice change={change} name={warned()} />
      </Show>
      <JobStatus step={change.step()} failure={change.failure()} />
    </Modal>
  );
}
