import { LoadingState, useApi, useI18n } from "@edelweiss/sdk";
import {
  createEffect,
  createMemo,
  createResource,
  createSignal,
  For,
  Match,
  on,
  onCleanup,
  Show,
  Switch,
} from "solid-js";
import { type LogSource, type LogsResponse, type ServiceView, servicePath } from "../api";
import { errorText, tSafe } from "../errors";
import { listen } from "../live";
import { Check, Modal } from "../Modal";

/** The most lines the view keeps: the oldest go first. */
export const MAX_LINES = 5000;

/** How close to the end the reader has to be for new lines to take the view along, in pixels. */
const AT_END = 24;

/** Calls fn once on the next animation frame, or after 100 ms where there are none; returns the cancel function. */
function onNextFrame(fn: () => void): () => void {
  if (typeof requestAnimationFrame === "function") {
    const id = requestAnimationFrame(() => fn());
    return () => cancelAnimationFrame(id);
  }
  const id = setTimeout(fn, 100);
  return () => clearTimeout(id);
}

/** Drops the oldest lines of buf once it holds twice the lines of the view: a batch at a time, not one per line. */
function trim(buf: string[]) {
  if (buf.length >= 2 * MAX_LINES) buf.splice(0, buf.length - MAX_LINES);
}

/** The last MAX_LINES of the lines. */
const newest = (lines: string[]) => (lines.length > MAX_LINES ? lines.slice(-MAX_LINES) : lines);

/** The log of a service below the API: of the source with this id, or of the first that can be read. */
const logsPath = (name: string, source?: string) =>
  `${servicePath(name)}/logs${source ? `?source=${encodeURIComponent(source)}` : ""}`;

/** The stream that follows the source from the byte offset of the answer. */
const streamPath = (name: string, source: string, offset: number) =>
  `${servicePath(name)}/logs/stream?source=${encodeURIComponent(source)}&offset=${offset}`;

/**
 * The logs of a service, for everybody: a source to choose (one that cannot be read is disabled and says that admin mode
 * reads it; each is worded by its id, not by the label of the backend, which is German for every user), the last 500
 * lines of it in a box that stays at the end, and "live", on at first, which follows the source from the offset of the
 * answer: the lines that come are appended (the newest 5000 stay) and a reset clears them. A stream that the browser opens
 * again after a loss, and one that is switched on again, get what came since that offset once more, so the view goes
 * back to the lines of the answer for it. A reader who scrolled up stays where they are.
 */
export function LogsView(props: { service: ServiceView; onClose: () => void }) {
  const api = useApi();
  const { t } = useI18n();
  const name = props.service.name;
  // the source asked for; none (the first that can be read) at first
  const [chosen, setChosen] = createSignal<string>();
  const [data] = createResource(
    () => ({ source: chosen() }),
    ({ source }) => api.get<LogsResponse>(logsPath(name, source)),
  );
  // data.latest throws the error of a failed load: ask for the error first
  const loaded = () => (data.error ? undefined : data.latest);
  // the answer for the source that is chosen, once it is there
  const answer = () => (data.loading ? undefined : loaded());
  // the sources of the last answer stay to choose from while another source loads, or fails to
  const sources = createMemo<LogSource[]>((last) => loaded()?.sources ?? last, []);
  const shownSource = () => chosen() ?? loaded()?.source ?? "";
  const [live, setLive] = createSignal(true);
  const [lines, setLines] = createSignal<string[]>([]);
  let box: HTMLPreElement | undefined;
  let atEnd = true; // whether the reader is at the end: new lines then take the view along

  createEffect(
    on(answer, (a) => {
      atEnd = true;
      setLines(a?.lines ?? []);
    }),
  );
  createEffect(
    on(lines, () => {
      if (atEnd && box) box.scrollTop = box.scrollHeight;
    }),
  );

  // Follows the source of the answer while live is on. The lines arrive one event each, often many at once: they wait in
  // pending and go into the view once per frame. The effect starts again, with a new stream, for every answer and every
  // time live is switched on.
  createEffect(() => {
    const a = answer();
    if (!live() || !a || a.source === "") return;
    const base = a.lines;
    setLines(base);
    let pending: string[] = [];
    let cancelFlush: (() => void) | undefined;
    const drop = () => {
      cancelFlush?.();
      cancelFlush = undefined;
      pending = [];
    };
    const flush = () => {
      cancelFlush = undefined;
      const more = pending;
      pending = [];
      if (more.length > 0) setLines((prev) => newest(prev.concat(more)));
    };
    listen(
      api.url(streamPath(name, a.source, a.offset)),
      {
        line: (line) => {
          if (typeof line !== "string") return;
          pending.push(line);
          trim(pending);
          cancelFlush ??= onNextFrame(flush);
        },
        reset: () => {
          drop();
          setLines([]);
        },
      },
      () => {
        drop();
        setLines(base);
      },
    );
    onCleanup(drop);
  });

  /** What a source is called: by its id, and where it cannot be read, that admin mode reads it. */
  const label = (s: LogSource) => {
    let text = s.id;
    if (s.id.startsWith("file:")) text = s.path;
    else if (s.id === "output" || s.id === "error") text = t(`logs.sources.${s.id}`);
    else if (s.id === "syslog") text = tSafe(t, "logs.sources.syslog", { name });
    return s.readable ? text : tSafe(t, "logs.unreadable", { source: text });
  };
  const shown = () => {
    const l = lines();
    if (l.length > 0) return l.join("\n");
    return data.loading ? t("loading") : t("logs.empty");
  };
  return (
    <Modal
      wide
      title={tSafe(t, "logs.title", { name })}
      onClose={props.onClose}
      actions={
        <button type="button" class="btn btn-primary" onClick={() => props.onClose()}>
          {t("common.close")}
        </button>
      }
    >
      <Show when={sources().length > 0}>
        <div class="flex flex-wrap items-center justify-between gap-3">
          <select
            class="select select-sm min-w-0 max-w-full"
            aria-label={t("logs.source")}
            onChange={(e) => setChosen(e.currentTarget.value)}
          >
            <For each={sources()}>
              {(s) => (
                <option value={s.id} disabled={!s.readable} selected={s.id === shownSource()}>
                  {label(s)}
                </option>
              )}
            </For>
          </select>
          <Check label={t("logs.live")} checked={live()} onChange={setLive} />
        </div>
      </Show>
      <Switch>
        <Match when={data.error}>
          {(err) => (
            <div class="alert alert-error text-sm" role="alert">
              {errorText(err(), t)}
            </div>
          )}
        </Match>
        <Match when={!loaded()}>
          <LoadingState label={t("loading")} />
        </Match>
        <Match when={loaded()?.sources.length === 0}>
          <p class="text-sm">{tSafe(t, "logs.none", { name })}</p>
        </Match>
        <Match when={!data.loading && loaded()?.source === ""}>
          <p class="text-sm">{tSafe(t, "logs.adminOnly", { name })}</p>
        </Match>
        <Match when={true}>
          <pre
            ref={box}
            class="h-96 overflow-auto whitespace-pre rounded-box bg-base-200 p-2 text-xs leading-5"
            onScroll={() => {
              if (box) atEnd = box.scrollHeight - box.scrollTop - box.clientHeight < AT_END;
            }}
          >
            {shown()}
          </pre>
          <p class="text-xs opacity-70">{t("logs.note")}</p>
        </Match>
      </Switch>
    </Modal>
  );
}
