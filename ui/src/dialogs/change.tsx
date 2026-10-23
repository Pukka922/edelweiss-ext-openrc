import { ApiError, useI18n } from "@edelweiss/sdk";
import { createSignal, Show } from "solid-js";
import type { Action, ServiceView } from "../api";
import { Buttons } from "../Buttons";
import { staleRefusal, tSafe, warnText } from "../errors";
import { Check, Modal } from "../Modal";
import { useJobRun } from "../useJob";

/** What the page gives a dialog of a change: the service, and the ways out of the dialog. */
export interface ChangeProps {
  /** The service as the view of the page had it when the dialog opened: the dialog is about that, not about the next view. */
  service: ServiceView;
  onClose(): void;
  /** The change is done, with the text to tell it; the page closes the dialog and reads the view again. */
  onDone(message: string): void;
  /** The view of the page is out of date (the service changed or is gone): read it again. */
  onReload(): void;
  /** Show the log of the service in place of the dialog. */
  onLogs(): void;
}

/**
 * The red warning a change waits for: the class that asks for it (reason), and the running services that stop along when
 * the server says so (a confirm_required names them), else not known.
 */
export interface Warning {
  reason: string;
  services?: string[];
}

const strings = (value: unknown): string[] | undefined =>
  Array.isArray(value) ? value.filter((v): v is string => typeof v === "string") : undefined;

/**
 * The job of a dialog of a change of the service of props, of kind: submit sends it (runlevel for enable and disable) and
 * the dialog tells the step, then done or the failure. A change the policy warns about from the start waits for the check
 * of "I know what I am doing" and then sends confirm: true; so does one the server asks the confirmation for after the
 * request (409 confirm_required: the warning of the class it names, and the services that stop along).
 *
 * A change is over when nothing is left to send: a request that is refused because the service changed or is gone (the
 * page then reads the view again; a second request would be refused again), a job that ended without success, and a job
 * whose state is not known (a second request could start it twice). The dialog then offers Close, and the way to the log
 * where the failed command left output, and no other try.
 */
export function useChange(props: ChangeProps, kind: Action) {
  const { t } = useI18n();
  const at = props.service;
  const [warning, setWarning] = createSignal<Warning | undefined>(
    at.allowed[kind] === "warn" ? { reason: at.class } : undefined,
  );
  const [acknowledged, setAcknowledged] = createSignal(false);
  const [over, setOver] = createSignal(false);
  const job = useJobRun(
    (message) => props.onDone(message),
    (err) => {
      if (err instanceof ApiError && err.code === "confirm_required") {
        const { reason, services } = err.details;
        setWarning({ reason: typeof reason === "string" ? reason : "", services: strings(services) });
        setAcknowledged(false);
        return true;
      }
      if (staleRefusal(err)) {
        setOver(true);
        props.onReload();
      }
      return false;
    },
  );
  const submit = (runlevel?: string) =>
    void job
      .run(
        {
          kind,
          service: at.name,
          ...(runlevel ? { runlevel } : {}),
          ...(warning() ? { confirm: true } : {}),
        },
        () => tSafe(t, `done.${kind}`, { name: at.name }),
      )
      .then((st) => {
        if (st ? st.status !== "done" : job.failure()?.neutral) setOver(true);
      });
  return {
    busy: job.busy,
    failure: job.failure,
    step: job.step,
    warning,
    acknowledged,
    setAcknowledged,
    submit,
    over,
    /** Whether a command of the job failed with output: the dialog then shows it, with the way to the log. */
    hasOutput: () => !!job.failure()?.output,
  };
}

export type Change = ReturnType<typeof useChange>;

/** The red callout of the warning of its class about the service named name, and the check the red button waits for. */
export function WarningNotice(props: { change: Change; name: string }) {
  const { t } = useI18n();
  return (
    <Show when={props.change.warning()}>
      {(warning) => (
        <>
          <div class="alert alert-error alert-soft text-sm" role="alert">
            {warnText(t, warning().reason, { name: props.name, host: window.location.hostname })}
          </div>
          <Check
            label={t("dialog.ack")}
            checked={props.change.acknowledged()}
            disabled={props.change.busy()}
            onChange={props.change.setAcknowledged}
          />
        </>
      )}
    </Show>
  );
}

/**
 * The buttons of a change: cancel and the change, which is red under a warning and waits for its check (and for
 * disabled); once the change is over, Close alone, with View log before it where a command failed with output.
 */
export function ChangeButtons(props: {
  change: Change;
  label: string;
  disabled?: boolean;
  onClose: () => void;
  onLogs: () => void;
  onConfirm: () => void;
}) {
  const { t } = useI18n();
  return (
    <Show
      when={!props.change.over()}
      fallback={
        <>
          <Show when={props.change.hasOutput()}>
            <button type="button" class="btn btn-ghost" onClick={() => props.onLogs()}>
              {t("dialog.viewLog")}
            </button>
          </Show>
          <button type="button" class="btn btn-primary" onClick={() => props.onClose()}>
            {t("common.close")}
          </button>
        </>
      }
    >
      <Buttons
        busy={props.change.busy()}
        danger={!!props.change.warning()}
        disabled={props.disabled || (!!props.change.warning() && !props.change.acknowledged())}
        label={props.label}
        onClose={props.onClose}
        onConfirm={props.onConfirm}
      />
    </Show>
  );
}

/** What a dialog shows for a change that does not fit the service any more: it says so and sends nothing. */
export function Changed(props: { title: string; onClose: () => void }) {
  const { t } = useI18n();
  return (
    <Modal
      title={props.title}
      onClose={props.onClose}
      actions={
        <button type="button" class="btn btn-primary" onClick={() => props.onClose()}>
          {t("common.close")}
        </button>
      }
    >
      <div class="alert alert-error text-sm" role="alert">
        {t("errors.field.changed")}
      </div>
    </Modal>
  );
}
