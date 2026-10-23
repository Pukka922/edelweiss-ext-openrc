import { useSession } from "@edelweiss/sdk";
import { onCleanup } from "solid-js";

/**
 * Opens a stream of server-sent events at url (see ApiClient.url) and calls the handler of each named event with its
 * JSON data; an event whose data is no JSON is skipped. The stream closes with the owner; the returned function closes
 * it earlier. A stream the browser gave up on may mean the session expired, so the shell is asked to check it.
 *
 * The browser connects again by itself after a failure, to the same url, and the server sends no event ids to resume
 * from: onReopen is called whenever the stream opens again after its first opening (not for the first one), so that the
 * owner can drop what the server sends again.
 */
export function listen(
  url: string,
  handlers: Record<string, (data: unknown) => void>,
  onReopen?: () => void,
): () => void {
  const session = useSession();
  const source = new EventSource(url);
  for (const [name, handle] of Object.entries(handlers)) {
    source.addEventListener(name, (e) => {
      const raw = (e as MessageEvent<unknown>).data;
      if (typeof raw !== "string") return;
      let data: unknown;
      try {
        data = JSON.parse(raw);
      } catch {
        return; // a malformed event
      }
      handle(data);
    });
  }
  let opened = false;
  source.onopen = () => {
    if (opened) onReopen?.();
    opened = true;
  };
  source.onerror = () => {
    if (source.readyState === EventSource.CLOSED) void session.refresh().catch(() => {});
  };
  const close = () => source.close();
  onCleanup(close);
  return close;
}
