import { useSession } from "@edelweiss/sdk";

/**
 * What comes before every change: act(open) asks for admin mode when it is off, and stops when it stays off. In admin
 * mode the process of the extension runs as root and may read more than before (the markers of the jobs and the system
 * log can be for root only), so the view is read again once before anything opens: reload resolves once the page has
 * it, and rejects when it could not be read (the page shows the error, and nothing opens). open runs last.
 */
export function useGuard(reload: () => Promise<unknown>): (open: () => void) => Promise<void> {
  const session = useSession();
  const reloaded = () =>
    reload().then(
      () => true,
      () => false,
    );
  return async (open) => {
    if (!session.admin()) {
      if (!(await session.requestAdmin())) return;
      if (!(await reloaded())) return;
    }
    open();
  };
}
