import {
  AdminRequired,
  ErrorAlert,
  LoadingState,
  PageHeader,
  useApi,
  useI18n,
  useSession,
  useToast,
} from "@edelweiss/sdk";
import { createMemo, createResource, createSignal, For, onCleanup, Show } from "solid-js";
import { CHANGES, hasProblem, inAutostart, isSystem, type ServiceView, type ViewResponse } from "./api";
import { DetailsDialog } from "./dialogs/DetailsDialog";
import { type Open, PageDialog } from "./dialogs/PageDialog";
import { agoText, errorText, tSafe } from "./errors";
import { useGuard } from "./guard";
import { JobBanner } from "./JobBanner";
import { ActionButton, type OnAction, ServiceRow, Spinner } from "./ServiceRow";
import { PageJob } from "./useJob";

/** How often the page reads the view again while it is visible, in milliseconds. */
const RELOAD_MS = 5000;

type Filter = "all" | "running" | "stopped" | "autostart";

const FILTERS: Filter[] = ["all", "running", "stopped", "autostart"];

/**
 * Which services each filter shows: those that run, those that are down (stopped, crashed or failed), those of the
 * autostart.
 */
const MATCHES: Record<Filter, (s: ServiceView) => boolean> = {
  all: () => true,
  running: (s) => s.state === "started",
  stopped: (s) => s.state === "stopped" || s.state === "crashed" || s.state === "failed",
  autostart: inAutostart,
};

const sameNames = (a: string[], b: string[]) => a.length === b.length && a.every((n, i) => n === b[i]);

/**
 * The page: the services of the view, read again every 5 seconds while the page is visible (and on "Reload"), the
 * running or interrupted job, a red banner for every service that crashed or whose start failed (with how long ago it
 * failed, when the view knows it, said anew with every view), the filters and the search, and the table. Every change
 * (start, stop, restart, the autostart) goes through the guard, which asks for admin mode first and reads the view
 * again, and then to the dialog of the change for the service as that view has it; the logs open their dialog at once,
 * and so do the details, for everybody. The dialogs know the job of the view (PageJob), and when one is done the page
 * tells it in a toast, closes it and reads the view again. For a user who cannot get admin mode the page has no button
 * that changes anything. Without OpenRC it says so and shows nothing else. While a change of a service runs, a spinner
 * takes the place of the button of its banner that changes it, next to the logs.
 *
 * openDialog takes the place of the dialogs of the logs and the changes, which a test uses to see what a click asks for.
 */
export function Services(props: { openDialog?: OnAction }) {
  const api = useApi();
  const session = useSession();
  const toast = useToast();
  const { t } = useI18n();
  // session.admin() reads the whole session object, which the shell replaces on every focus refresh
  const admin = createMemo(() => session.admin());
  // a user who can never get admin mode sees the page read-only: nothing would do anything for them
  const canAdmin = createMemo(() => session.canAdmin());
  const [data, { refetch }] = createResource(() => api.get<ViewResponse>("/view"));
  // the last view that came: a reload that fails leaves it on the page, below the error
  const view = createMemo<ViewResponse | undefined>((last) => (data.error ? last : (data.latest ?? last)));
  const timer = setInterval(() => {
    if (document.visibilityState === "visible" && !data.loading) void refetch();
  }, RELOAD_MS);
  onCleanup(() => clearInterval(timer));

  const services = () => view()?.services ?? [];
  // the moment of the newest view: how long ago a start failed is said anew with every view
  const seen = createMemo(() => {
    view();
    return Date.now();
  });
  const byName = createMemo(() => new Map(services().map((s) => [s.name, s])));

  // After admin mode the guard reads the view again. refetch does not reject when the read fails (the resource keeps the
  // error), so the failure is thrown here: nothing opens then, and the error stays on the page.
  const guard = useGuard(async () => {
    await refetch();
    if (data.error) throw data.error;
  });
  const [filter, setFilter] = createSignal<Filter>("all");
  const [search, setSearch] = createSignal("");
  const [showSystem, setShowSystem] = createSignal(false);
  const [details, setDetails] = createSignal<string>();
  // the dialog of the logs or of a change that is open, with the services of the view it opened in
  const [open, setOpen] = createSignal<Open>();
  const openOwn: OnAction = (kind, service) => {
    if (kind !== "details") setOpen({ kind, service, services: services() });
  };
  const done = (message: string) => {
    toast.success(message);
    setOpen(undefined);
    void refetch();
  };
  const onAction: OnAction = (kind, service) => {
    // the dialog is about the service as the newest view has it (admin mode may have read it again); one that has left
    // the view opens nothing
    const show = () => {
      const current = byName().get(service.name);
      if (!current) return;
      if (kind === "details") setDetails(current.name);
      else (props.openDialog ?? openOwn)(kind, current);
    };
    // a change asks for admin mode first; the logs and the details only read
    if (CHANGES.includes(kind)) void guard(show);
    else show();
  };

  const found = (s: ServiceView) => {
    const q = search().trim().toLowerCase();
    return q === "" || s.name.toLowerCase().includes(q) || s.description.toLowerCase().includes(q);
  };
  const shown = (s: ServiceView) => (showSystem() || !isSystem(s)) && MATCHES[filter()](s) && found(s);
  // Rows and banners are by the name of their service, not by position: a new view every few seconds keeps them and
  // their focus, also when a filter lets the services above one go.
  const namesWhere = (keep: (s: ServiceView) => boolean) =>
    createMemo<string[]>(() => services().flatMap((s) => (keep(s) ? [s.name] : [])), [], {
      equals: sameNames,
    });
  const listed = namesWhere(shown);
  const problems = namesWhere(hasProblem);
  /** The service of that name in the newest view; once it has left the view, the last one the row or banner had. */
  const track = (name: string) => createMemo<ServiceView>((last) => byName().get(name) ?? last!);
  /** Whether a job for the service runs. */
  const working = (name: string) => {
    const job = view()?.job;
    return job?.status === "running" && job.target === name;
  };

  const subtitle = () => {
    const v = view();
    if (!v?.present) return undefined;
    const count = (key: string, n: number) =>
      n === 1 ? t(`subtitle.${key}One`) : tSafe(t, `subtitle.${key}`, { n });
    return [
      count("services", v.counts.total),
      count("running", v.counts.running),
      v.counts.problems > 0 ? count("problems", v.counts.problems) : "",
    ]
      .filter(Boolean)
      .join(" · ");
  };

  return (
    <PageJob.Provider value={() => view()?.job}>
      <div class="flex flex-col gap-4">
        <PageHeader
          title={t("title")}
          subtitle={subtitle()}
          actions={
            <Show when={view()?.present !== false}>
              <button type="button" class="btn btn-sm" onClick={() => void refetch()}>
                {t("actions.reload")}
              </button>
            </Show>
          }
        />
        <Show when={data.error}>
          {(err) => <ErrorAlert error={errorText(err(), t)} onRetry={() => void refetch()} />}
        </Show>
        <Show
          when={view()}
          fallback={
            <Show when={!data.error}>
              <LoadingState label={t("loading")} />
            </Show>
          }
        >
          {(v) => (
            <Show when={v().present} fallback={<p class="text-base-content/70">{t("noOpenRC")}</p>}>
              <Show when={v().job}>{(job) => <JobBanner job={job()} />}</Show>
              <For each={problems()}>
                {(name) => {
                  const s = track(name);
                  // a crashed service is restarted, one whose start failed is started
                  const fix = () => (s().state === "failed" ? "start" : "restart");
                  const text = () => {
                    if (s().state !== "failed") return tSafe(t, "banner.crashed", { name });
                    const failedAt = s().failedAt;
                    const ago = failedAt === undefined ? undefined : agoText(t, failedAt, seen());
                    return ago === undefined
                      ? tSafe(t, "banner.failed", { name })
                      : tSafe(t, "banner.failedAt", { name, ago });
                  };
                  return (
                    <div class="alert alert-error" role="alert">
                      <span>{text()}</span>
                      <div class="flex items-center">
                        <ActionButton kind="logs" service={s()} onAction={onAction} />
                        <Show when={!working(name)} fallback={<Spinner />}>
                          <Show when={canAdmin() && s().allowed[fix()] !== "deny"}>
                            <ActionButton kind={fix()} service={s()} onAction={onAction} />
                          </Show>
                        </Show>
                      </div>
                    </div>
                  );
                }}
              </For>
              <div class="flex flex-wrap items-center justify-between gap-2">
                <fieldset class="join" aria-label={t("filters.label")}>
                  <For each={FILTERS}>
                    {(f) => (
                      <button
                        type="button"
                        class="btn btn-sm join-item"
                        classList={{ "btn-active": filter() === f }}
                        aria-pressed={filter() === f}
                        onClick={() => setFilter(f)}
                      >
                        {t(`filters.${f}`)}
                      </button>
                    )}
                  </For>
                </fieldset>
                <input
                  type="search"
                  class="input input-sm w-60 max-w-full"
                  placeholder={t("search")}
                  aria-label={t("search")}
                  value={search()}
                  onInput={(e) => setSearch(e.currentTarget.value)}
                />
              </div>
              <section class="card card-border bg-base-100">
                <div class="card-body gap-3 p-4">
                  <Show when={listed().length > 0} fallback={<p class="text-sm opacity-70">{t("empty")}</p>}>
                    <div class="overflow-x-auto">
                      <table class="table table-sm">
                        <thead>
                          <tr>
                            <th>{t("columns.service")}</th>
                            <th>{t("columns.status")}</th>
                            <th>{t("columns.autostart")}</th>
                            <th>
                              <span class="sr-only">{t("actions.label")}</span>
                            </th>
                          </tr>
                        </thead>
                        <tbody>
                          <For each={listed()}>
                            {(name) => {
                              const s = track(name);
                              return (
                                <ServiceRow
                                  service={s()}
                                  readOnly={!canAdmin()}
                                  busy={working(name)}
                                  onAction={onAction}
                                />
                              );
                            }}
                          </For>
                        </tbody>
                      </table>
                    </div>
                  </Show>
                  <div class="flex flex-wrap items-center gap-x-4 gap-y-1">
                    <label class="flex items-center gap-2 text-sm">
                      <input
                        type="checkbox"
                        class="checkbox checkbox-sm"
                        checked={showSystem()}
                        onChange={(e) => setShowSystem(e.currentTarget.checked)}
                      />
                      {t("system.show")}
                    </label>
                    <span class="text-xs opacity-70">{t("system.hint")}</span>
                  </div>
                </div>
              </section>
              <Show when={!admin()}>
                <AdminRequired message={canAdmin() ? t("adminHint") : t("adminOnly")} />
              </Show>
            </Show>
          )}
        </Show>
        <Show when={details()} keyed>
          {(name) => <DetailsDialog name={name} onClose={() => setDetails(undefined)} />}
        </Show>
        <Show when={open()} keyed>
          {(d) => (
            <PageDialog
              open={d}
              onClose={() => setOpen(undefined)}
              onDone={done}
              onReload={() => void refetch()}
              onLogs={() => setOpen({ ...d, kind: "logs" })}
            />
          )}
        </Show>
      </div>
    </PageJob.Provider>
  );
}
