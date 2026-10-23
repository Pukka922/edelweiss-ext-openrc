import type { fakeSession } from "@edelweiss/sdk/testing";
import { within } from "@solidjs/testing-library";
import { vi } from "vitest";
import type { Kind, ServiceView, ViewResponse } from "../api";
import { Services } from "../Services";
import { counts, view } from "./fixtures";
import { type Handler, json, renderExt, stubApi } from "./render";

/** What the page shows by default: every service but the system services, by name. */
export const VISIBLE = [
  "chronyd",
  "cronie",
  "cups",
  "docker",
  "edelweiss",
  "mdadm-raid",
  "net.eth0",
  "networking",
  "nginx",
  "redis",
  "rsyslog",
  "samba",
  "sshd",
];

export type R = ReturnType<typeof renderExt>;

/** The name in a row of the table. */
const nameOf = (tr: HTMLElement) => tr.querySelector("td .font-semibold")?.textContent;

/** The names of the services in the table, in order. */
export const names = (r: R) => r.queryAllByRole("row").flatMap((tr) => nameOf(tr) ?? []);

/** The row of a service. */
export const row = (r: R, name: string) => r.getAllByRole("row").find((tr) => nameOf(tr) === name)!;

/** The alert whose text holds text. */
export const alertWith = (r: R, text: string) =>
  r.queryAllByRole("alert").find((a) => a.textContent?.includes(text));

/** The labels of the buttons in el. */
export const labels = (el: HTMLElement) =>
  within(el)
    .queryAllByRole("button")
    .map((b) => b.getAttribute("aria-label"));

/** A view of the services of list, counted as the server does. */
export const viewOf = (list: ServiceView[], over: Partial<ViewResponse> = {}): ViewResponse => ({
  ...view(),
  services: list,
  counts: counts(list),
  ...over,
});

/** The page with the view of the preview's host (or v), and a recorder for the dialog hook. */
export function renderPage(
  opts: {
    session?: ReturnType<typeof fakeSession>;
    locale?: "en" | "de";
    v?: ViewResponse;
    routes?: Record<string, Handler>;
  } = {},
) {
  const api = stubApi({ "GET /view": () => json(opts.v ?? view()), ...opts.routes });
  const openDialog = vi.fn<(kind: Kind, s: ServiceView) => void>();
  const r = renderExt(() => <Services openDialog={openDialog} />, opts.session, opts.locale);
  return { ...r, api, openDialog };
}
