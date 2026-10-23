import { LoadingState, useApi, useI18n } from "@edelweiss/sdk";
import { createResource, type JSX, Show } from "solid-js";
import { type Details, servicePath } from "../api";
import { errorText, stateText, tSafe } from "../errors";
import { Modal } from "../Modal";

/** What the dialog shows for an empty list or an empty text. */
const NONE = "–";

/** The names of a relation, or a dash when there are none. */
const names = (list: string[] | undefined) => (list?.length ? list.join(", ") : NONE);

/**
 * The details of a service, read from GET /services/<name> when the dialog opens (the relations are asked of OpenRC on
 * demand, so they are not in the list): the description, the status with the uptime and the PID when OpenRC tells them,
 * whether it is supervised, the runlevels of its autostart, what it needs, uses, starts after and is needed by, and its
 * script. Everybody may open it.
 */
export function DetailsDialog(props: { name: string; onClose: () => void }) {
  const api = useApi();
  const { t } = useI18n();
  const [data] = createResource(
    () => props.name,
    (name) => api.get<Details>(servicePath(name)),
  );
  // data.latest throws the error of a failed load: ask for the error first
  const loaded = () => (data.error ? undefined : data.latest);
  const status = (d: Details) =>
    [
      stateText(t, d),
      d.uptime ? tSafe(t, "details.uptime", { uptime: d.uptime }) : "",
      d.pid ? tSafe(t, "details.pid", { pid: d.pid }) : "",
    ]
      .filter(Boolean)
      .join(" · ");
  const entry = (label: string, value: JSX.Element) => (
    <>
      <dt class="opacity-70">{t(`details.${label}`)}</dt>
      <dd class="break-words">{value}</dd>
    </>
  );
  return (
    <Modal
      title={tSafe(t, "details.title", { name: props.name })}
      onClose={props.onClose}
      actions={
        <button type="button" class="btn btn-primary" onClick={() => props.onClose()}>
          {t("common.close")}
        </button>
      }
    >
      <Show when={data.error}>
        {(err) => (
          <div class="alert alert-error" role="alert">
            {errorText(err(), t)}
          </div>
        )}
      </Show>
      <Show
        when={loaded()}
        fallback={
          <Show when={!data.error}>
            <LoadingState label={t("loading")} />
          </Show>
        }
      >
        {(d) => (
          <dl class="grid grid-cols-[8rem_1fr] gap-x-3 gap-y-1 text-sm">
            {entry("description", d().description || NONE)}
            {entry("status", status(d()))}
            {entry("supervised", d().supervised ? t("details.supervisedYes") : t("details.supervisedNo"))}
            {entry("autostart", d().runlevels.length ? d().runlevels.join(", ") : t("row.autostartOff"))}
            {entry("needs", names(d().needs))}
            {entry("uses", names(d().uses))}
            {entry("after", names(d().after))}
            {entry("neededBy", names(d().neededBy))}
            {entry("script", <code class="text-xs">{d().script}</code>)}
          </dl>
        )}
      </Show>
    </Modal>
  );
}
