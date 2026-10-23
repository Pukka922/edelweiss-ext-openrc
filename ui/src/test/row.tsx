import { vi } from "vitest";
import type { Kind, ServiceView } from "../api";
import { ServiceRow } from "../ServiceRow";
import { service } from "./fixtures";
import { renderExt } from "./render";

export type Over = Parameters<typeof service>[0];

/** A row for the service the over describes (a service of the view as well), in a table. */
export function renderRow(
  over: Over,
  opts: { readOnly?: boolean; busy?: boolean; locale?: "en" | "de" } = {},
) {
  const onAction = vi.fn<(kind: Kind, s: ServiceView) => void>();
  const s = service(over);
  const r = renderExt(
    () => (
      <table>
        <tbody>
          <ServiceRow service={s} readOnly={!!opts.readOnly} busy={opts.busy} onAction={onAction} />
        </tbody>
      </table>
    ),
    undefined,
    opts.locale,
  );
  return { ...r, onAction, service: s };
}
