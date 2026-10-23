import { ApiError, createApiClient, useApi, useI18n } from "@edelweiss/sdk";
import { createSignal, onCleanup, Show } from "solid-js";
import type { JobRequest, ServiceView } from "../api";
import { Buttons } from "../Buttons";
import { errorText, staleRefusal, tSafe } from "../errors";
import { Modal } from "../Modal";

/** How often the page asks the core whether edelweiss answers again, in milliseconds. */
export const RESTART_POLL_MS = 2000;

/** How long the page waits for the restart to show before it reloads anyway, in milliseconds. */
export const RESTART_GIVE_UP_MS = 30_000;

/**
 * Whether the core is down: it gave no answer at all (a network error), or the 502 or 503 that a proxy gives for it. Any
 * other answer, an error status or an answer that is no JSON, is an answer of a core that runs.
 */
const down = (err: unknown) =>
  err instanceof ApiError ? err.status === 502 || err.status === 503 : !(err instanceof SyntaxError);

/** Whether an error is the 401 of a core that has lost its sessions, which is what the restart does to every one. */
const signedOut = (err: unknown) => err instanceof ApiError && err.status === 401;

/**
 * Restarts edelweiss itself, the one service that is restarted but never stopped. It tells that the page is gone for a few
 * seconds and that everybody has to log in again, and once the job is accepted it shows an overlay in place of the dialog
 * and asks GET /api/session of the core every 2 seconds. It reloads the page once the restart has been seen: at a 401
 * (the restart signs everybody out), or at an answer after the core was down (no answer, 502 or 503) before; the
 * answers of the old process, which runs until it stops, are no reason to reload. After 30 seconds it reloads whatever it
 * has seen. It does not wait for the job: the extension goes away with edelweiss, and the job runs on without it. A
 * request that is refused because the service changed or is gone makes the page read the view again, and leaves only Close.
 */
export function RestartEdelweissDialog(props: {
  service: ServiceView;
  onClose: () => void;
  /** The view of the page is out of date (the service changed or is gone): read it again. */
  onReload: () => void;
}) {
  const { t } = useI18n();
  const api = useApi();
  const core = createApiClient({ base: "/api", csrfToken: () => undefined });
  const name = props.service.name;
  const [busy, setBusy] = createSignal(false);
  const [error, setError] = createSignal<string>();
  const [waiting, setWaiting] = createSignal(false);
  // the request that the service changed or is gone refused: nothing is left to send
  const [over, setOver] = createSignal(false);
  let timer: ReturnType<typeof setTimeout> | undefined;
  let giveUp: ReturnType<typeof setTimeout> | undefined;
  let seenDown = false;
  let alive = true;
  const stop = () => {
    alive = false;
    clearTimeout(timer);
    clearTimeout(giveUp);
  };
  onCleanup(stop);
  const reload = () => {
    if (!alive) return;
    stop();
    window.location.reload();
  };
  const poll = async () => {
    let answered = true;
    let signedOutNow = false;
    try {
      await core.get("/session");
    } catch (err) {
      answered = !down(err);
      signedOutNow = signedOut(err);
    }
    if (!alive) return;
    if (!answered) seenDown = true;
    else if (seenDown || signedOutNow) return reload();
    wait();
  };
  const wait = () => {
    timer = setTimeout(() => void poll(), RESTART_POLL_MS);
  };
  const restart = async () => {
    setBusy(true);
    setError(undefined);
    try {
      await api.post("/jobs", { kind: "restart", service: name } satisfies JobRequest);
      setWaiting(true);
      wait();
      giveUp = setTimeout(reload, RESTART_GIVE_UP_MS);
    } catch (err) {
      setError(errorText(err, t));
      if (staleRefusal(err)) {
        setOver(true);
        props.onReload();
      }
    } finally {
      setBusy(false);
    }
  };
  return (
    <Show
      when={waiting()}
      fallback={
        <Modal
          title={tSafe(t, "action.title.restart", { name })}
          busy={busy()}
          onClose={props.onClose}
          actions={
            <Show
              when={!over()}
              fallback={
                <button type="button" class="btn btn-primary" onClick={() => props.onClose()}>
                  {t("common.close")}
                </button>
              }
            >
              <Buttons
                busy={busy()}
                label={t("actions.restart")}
                onClose={props.onClose}
                onConfirm={() => void restart()}
              />
            </Show>
          }
        >
          <div class="rounded-box bg-base-200 p-3 text-sm">{t("restartEdelweiss.callout")}</div>
          <p>{t("restartEdelweiss.note")}</p>
          <Show when={error()}>
            {(text) => (
              <div class="alert alert-error text-sm" role="alert">
                {text()}
              </div>
            )}
          </Show>
        </Modal>
      }
    >
      <div
        class="fixed inset-0 z-50 flex flex-col items-center justify-center gap-4 bg-base-100"
        role="status"
      >
        <span class="loading loading-spinner loading-lg" aria-hidden="true" />
        <p class="text-lg">{t("restartEdelweiss.waiting")}</p>
      </div>
    </Show>
  );
}
