import type { render } from "@solidjs/testing-library";
import { vi } from "vitest";
import type { Action, JobState, ServiceView } from "../api";
import { ActionDialog, type ActionKind } from "../dialogs/ActionDialog";
import { AutostartDialog, type AutostartKind } from "../dialogs/AutostartDialog";
import { details, view } from "./fixtures";
import { jobRoutes, json, renderExt } from "./render";

/** The hooks a dialog of a change gets from the page, recorded. */
const hooks = () => ({ onClose: vi.fn(), onDone: vi.fn(), onReload: vi.fn(), onLogs: vi.fn() });

/** The ActionDialog of kind for s, in the view of the preview's host (or services); the hooks are recorded. */
export function renderAction(
  kind: ActionKind,
  s: ServiceView,
  opts: { locale?: "en" | "de"; services?: ServiceView[] } = {},
) {
  const h = hooks();
  const services = opts.services ?? view().services;
  const r = renderExt(
    () => <ActionDialog kind={kind} service={s} services={services} {...h} />,
    undefined,
    opts.locale,
  );
  return { ...r, ...h };
}

/** The AutostartDialog of kind for s; the hooks are recorded. */
export function renderAutostart(kind: AutostartKind, s: ServiceView, locale?: "en" | "de") {
  const h = hooks();
  const r = renderExt(() => <AutostartDialog kind={kind} service={s} {...h} />, undefined, locale);
  return { ...r, ...h };
}

/** A job that is done at its first poll. */
export const DONE: Partial<JobState>[] = [{ status: "done" }];

/**
 * The routes of a job of kind on s that ends as states say (done by default), and the relations of s (neededBy are the
 * services that need it). One set for the dialogs of the actions and of the autostart.
 */
export function routes(kind: Action, s: ServiceView, states = DONE, neededBy: string[] = []) {
  return jobRoutes(kind, s.name, [kind], states, {
    [`GET /services/${s.name}`]: () => json(details(s.name, { neededBy })),
  });
}

type Rendered = Pick<ReturnType<typeof render>, "getByRole">;

/** The button of the dialog with that name. */
export const button = (r: Rendered, name: string) => r.getByRole("button", { name }) as HTMLButtonElement;

/** The dialog with that name. */
export const dialog = (r: Rendered, name: string) => r.getByRole("dialog", { name });

/** The select of the runlevel of the autostart dialog. */
export const levelSelect = (r: Rendered) =>
  r.getByRole("combobox", { name: "Runlevel" }) as HTMLSelectElement;
