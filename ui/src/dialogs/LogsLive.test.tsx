import { FakeEventSource } from "@edelweiss/sdk/testing";
import { cleanup, fireEvent, waitFor } from "@solidjs/testing-library";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { answer, box, line, loaded, renderLogs, shown, stream } from "../test/logs";
import { json, stubEventSource } from "../test/render";
import { MAX_LINES } from "./LogsView";

beforeEach(() => {
  stubEventSource();
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("LogsView", () => {
  describe("live", () => {
    it("is on, and follows the source from the offset of the answer, below the API of the extension", async () => {
      const r = renderLogs();
      await loaded(r);
      expect((r.getByRole("checkbox", { name: "Follow live" }) as HTMLInputElement).checked).toBe(true);
      await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
      expect(stream().url).toBe(
        "/ext/openrc/api/services/docker/logs/stream?source=file%3Adocker.log&offset=4096",
      );
    });

    it("appends the lines of the line events, which carry a JSON string", async () => {
      const r = renderLogs();
      await loaded(r);
      await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
      stream().open();
      line(stream(), "line 4");
      line(stream(), 'quote " and $& and <b>tag</b>');
      await waitFor(() =>
        expect(shown(r)).toBe('line 1\nline 2\nline 3\nline 4\nquote " and $& and <b>tag</b>'),
      );
      expect(box(r).querySelector("b")).toBeNull();
    });

    it("ignores an event that is no string or no JSON", async () => {
      const r = renderLogs();
      await loaded(r);
      await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
      stream().open();
      stream().emit("line", JSON.stringify({ line: "an object" }));
      stream().emit("line", "not json");
      stream().emit("line", "42");
      line(stream(), "the one that counts");
      await waitFor(() => expect(shown(r)).toBe("line 1\nline 2\nline 3\nthe one that counts"));
    });

    it("clears the lines at a reset, and shows the lines that follow it", async () => {
      const r = renderLogs();
      await loaded(r);
      await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
      stream().open();
      line(stream(), "line 4");
      await waitFor(() => expect(shown(r)).toContain("line 4"));
      stream().emit("reset", "{}");
      await waitFor(() => expect(shown(r)).toBe("No lines yet."));
      line(stream(), "fresh 1");
      line(stream(), "fresh 2");
      await waitFor(() => expect(shown(r)).toBe("fresh 1\nfresh 2"));
    });

    it("drops the lines that came just before a reset: they were of the old file", async () => {
      const r = renderLogs();
      await loaded(r);
      await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
      stream().open();
      line(stream(), "old, not shown yet");
      stream().emit("reset", "{}");
      line(stream(), "new");
      await waitFor(() => expect(shown(r)).toBe("new"));
    });

    it(`keeps at most ${MAX_LINES} lines: the newest`, async () => {
      expect(MAX_LINES).toBe(5000);
      const first = Array.from({ length: 500 }, (_, i) => `old ${i}`);
      const r = renderLogs({ "GET /services/docker/logs": () => json(answer({ lines: first })) });
      await loaded(r);
      await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
      stream().open();
      for (let i = 0; i < 5200; i++) line(stream(), `new ${i}`);
      await waitFor(() => expect(shown(r)?.endsWith("new 5199")).toBe(true));
      const lines = shown(r)?.split("\n") ?? [];
      expect(lines).toHaveLength(5000);
      expect(lines[0]).toBe("new 200");
      expect(lines.includes("old 499")).toBe(false);
    });

    it("closes the stream when it is switched off, and follows again from the answer when it is switched on", async () => {
      const r = renderLogs();
      await loaded(r);
      await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
      stream().open();
      line(stream(), "line 4");
      await waitFor(() => expect(shown(r)).toContain("line 4"));
      fireEvent.click(r.getByRole("checkbox", { name: "Follow live" }));
      expect(stream(0).readyState).toBe(FakeEventSource.CLOSED);
      expect(shown(r)).toContain("line 4"); // what is there stays
      fireEvent.click(r.getByRole("checkbox", { name: "Follow live" }));
      await waitFor(() => expect(FakeEventSource.instances).toHaveLength(2));
      expect(stream(1).url).toBe(stream(0).url);
      // the server sends what came since the offset again: the lines of the first stream are not shown twice
      expect(shown(r)).toBe("line 1\nline 2\nline 3");
      stream(1).open();
      line(stream(1), "line 4");
      await waitFor(() => expect(shown(r)).toBe("line 1\nline 2\nline 3\nline 4"));
    });

    it("shows the lines of the answer from the start when the browser connects again after a loss", async () => {
      const r = renderLogs();
      await loaded(r);
      await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
      stream().open();
      line(stream(), "line 4");
      await waitFor(() => expect(shown(r)).toContain("line 4"));
      // the connection broke; the browser connects again to the same URL, and the server follows from the same offset
      stream().fail(false);
      stream().open();
      expect(shown(r)).toBe("line 1\nline 2\nline 3");
      line(stream(), "line 4");
      line(stream(), "line 5");
      await waitFor(() => expect(shown(r)).toBe("line 1\nline 2\nline 3\nline 4\nline 5"));
    });

    it("opens no stream when it is off, and not before the first answer", async () => {
      const r = renderLogs();
      expect(FakeEventSource.instances).toEqual([]);
      await loaded(r);
      fireEvent.click(r.getByRole("checkbox", { name: "Follow live" }));
      await waitFor(() => expect(stream(0).readyState).toBe(FakeEventSource.CLOSED));
      expect(FakeEventSource.instances).toHaveLength(1);
    });

    it("closes the stream when the view is gone", async () => {
      const r = renderLogs();
      await loaded(r);
      await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
      r.unmount();
      expect(stream().readyState).toBe(FakeEventSource.CLOSED);
    });

    it("asks the core whether the session is gone when the stream is given up", async () => {
      const r = renderLogs();
      await loaded(r);
      await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
      stream().fail(true);
      expect(r.session.calls.refresh).toBe(1);
      stream().fail(false);
      expect(r.session.calls.refresh).toBe(1);
    });
  });
});
