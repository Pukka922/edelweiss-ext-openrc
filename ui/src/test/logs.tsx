import { FakeEventSource } from "@edelweiss/sdk/testing";
import { waitFor } from "@solidjs/testing-library";
import { expect, vi } from "vitest";
import type { LogSource, LogsResponse } from "../api";
import { LogsView } from "../dialogs/LogsView";
import { docker } from "./fixtures";
import { type Handler, json, renderExt, stubApi } from "./render";

export const FILE: LogSource = {
  id: "file:docker.log",
  label: "/var/log/docker.log",
  path: "/var/log/docker.log",
  readable: true,
};
export const SYSLOG: LogSource = {
  id: "syslog",
  label: "/var/log/messages (nur docker)",
  path: "/var/log/messages",
  readable: true,
};

/** The answer of GET /services/docker/logs: the file, its three lines, the offset 4096. */
export const answer = (over: Partial<LogsResponse> = {}): LogsResponse => ({
  sources: [FILE, SYSLOG],
  source: FILE.id,
  lines: ["line 1", "line 2", "line 3"],
  offset: 4096,
  capped: false,
  ...over,
});

/** The LogsView of s (docker), with the logs routes of the test; onClose is recorded. */
export function renderLogs(routes: Record<string, Handler> = {}, opts: { locale?: "en" | "de" } = {}) {
  const api = stubApi({ "GET /services/docker/logs": () => json(answer()), ...routes });
  const onClose = vi.fn();
  const r = renderExt(() => <LogsView service={docker()} onClose={onClose} />, undefined, opts.locale);
  return { ...r, api, onClose };
}

export type R = ReturnType<typeof renderLogs>;

export const box = (r: Pick<R, "container">) =>
  r.container.ownerDocument.querySelector("pre") as HTMLPreElement;
export const shown = (r: Pick<R, "container">) => box(r).textContent;
export const sourceSelect = (r: R, name = "Source") => r.getByRole("combobox", { name }) as HTMLSelectElement;
export const optionTexts = (el: HTMLSelectElement) => Array.from(el.options).map((o) => o.textContent);
export const stream = (i = 0) => FakeEventSource.instances[i] as FakeEventSource;
/** Waits until the first answer is shown. */
export const loaded = (r: R) => waitFor(() => expect(r.queryByRole("combobox")).toBeTruthy());
export const line = (s: FakeEventSource, text: string) => s.emit("line", JSON.stringify(text));
