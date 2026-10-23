import type { Kind, ServiceView } from "../api";
import { ActionDialog } from "./ActionDialog";
import { AutostartDialog } from "./AutostartDialog";
import { LogsView } from "./LogsView";
import { RestartEdelweissDialog } from "./RestartEdelweissDialog";

/** The dialog the page has open: the kind of the click, the service as the view had it, and the services of that view. */
export interface Open {
  kind: Exclude<Kind, "details">;
  service: ServiceView;
  services: ServiceView[];
}

/**
 * The dialog for what was clicked: the logs, the autostart (enable and disable), the restart of edelweiss itself (it is
 * never stopped, and its restart ends the session), or the start, stop and restart of any other service. The page builds
 * it afresh for every click, and it is about the service as it was then.
 */
export function PageDialog(props: {
  open: Open;
  onClose: () => void;
  onDone: (message: string) => void;
  onReload: () => void;
  onLogs: () => void;
}) {
  const { kind, service, services } = props.open;
  const change = {
    service,
    onClose: props.onClose,
    onDone: props.onDone,
    onReload: props.onReload,
    onLogs: props.onLogs,
  };
  if (kind === "logs") return <LogsView service={service} onClose={props.onClose} />;
  if (kind === "enable" || kind === "disable") return <AutostartDialog kind={kind} {...change} />;
  if (kind === "restart" && service.class === "edelweiss")
    return <RestartEdelweissDialog service={service} onClose={props.onClose} onReload={props.onReload} />;
  return <ActionDialog kind={kind} services={services} {...change} />;
}
