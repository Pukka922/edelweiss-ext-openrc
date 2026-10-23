import { FakeEventSource } from "@edelweiss/sdk/testing";
import { cleanup, fireEvent, waitFor } from "@solidjs/testing-library";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { LogSource } from "../api";
import { service } from "../test/fixtures";
import {
  answer,
  box,
  FILE,
  line,
  loaded,
  optionTexts,
  renderLogs,
  SYSLOG,
  shown,
  sourceSelect,
  stream,
} from "../test/logs";
import { apiError, json, renderExt, stubApi, stubEventSource } from "../test/render";
import { LogsView } from "./LogsView";

beforeEach(() => {
  stubEventSource();
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("LogsView", () => {
  it("loads the log of the service and shows its last lines in a pre", async () => {
    const r = renderLogs();
    expect(r.getByRole("dialog", { name: "Logs: docker" })).toBeTruthy();
    expect(r.getByRole("status")).toBeTruthy(); // loading
    await loaded(r);
    expect(r.api.sent("GET", "/services/docker/logs")).toHaveLength(1);
    expect(shown(r)).toBe("line 1\nline 2\nline 3");
    expect(r.queryByRole("status")).toBeNull();
  });

  it("names the service in the title, escaped once, with its dollar sign", async () => {
    const odd = service({ name: "a+b$&" });
    const api = stubApi({ "GET /services/a%2Bb%24%26/logs": () => json(answer({ lines: ["x"] })) });
    const r = renderExt(() => <LogsView service={odd} onClose={() => {}} />);
    expect(r.getByRole("dialog", { name: "Logs: a+b$&" })).toBeTruthy();
    await waitFor(() => expect(shown(r)).toBe("x"));
    expect(api.sent("GET", "/services/a%2Bb%24%26/logs")).toHaveLength(1);
  });

  it("says the last 500 lines, and what live does", async () => {
    const r = renderLogs();
    await loaded(r);
    expect(
      r.getByText(
        "The last 500 lines; “follow live” adds new lines at the bottom. Where a service has no log of its own, the page shows its lines from the system log.",
      ),
    ).toBeTruthy();
  });

  it("speaks German", async () => {
    const r = renderLogs({}, { locale: "de" });
    expect(r.getByRole("dialog", { name: "Logs: docker" })).toBeTruthy();
    await waitFor(() => expect(r.queryByRole("combobox", { name: "Quelle" })).toBeTruthy());
    expect(r.getByRole("checkbox", { name: "live mitlesen" })).toBeTruthy();
    expect(r.getByRole("button", { name: "Schließen" })).toBeTruthy();
    expect(r.getByText(/Die letzten 500 Zeilen/)).toBeTruthy();
  });

  it("closes with its button and with Escape", async () => {
    const r = renderLogs();
    await loaded(r);
    fireEvent.click(r.getByRole("button", { name: "Close" }));
    expect(r.onClose).toHaveBeenCalledTimes(1);
    fireEvent.keyDown(document, { key: "Escape" });
    expect(r.onClose).toHaveBeenCalledTimes(2);
  });

  describe("the sources", () => {
    // the backend writes the label of every source in German for every user: the page words them by their id
    const SOURCES: LogSource[] = [
      { id: "output", label: "/var/log/docker/out.log", path: "/var/log/docker/out.log", readable: true },
      { id: "error", label: "/var/log/docker/err.log", path: "/var/log/docker/err.log", readable: true },
      FILE,
      { id: "file:docker/app.log", label: "app", path: "/var/log/docker/app.log", readable: true },
      SYSLOG,
    ];

    it("are labelled by their id: the output log, the error log, the path of a file, the system log of the service", async () => {
      const r = renderLogs({
        "GET /services/docker/logs": () => json(answer({ sources: SOURCES, source: "output" })),
      });
      await loaded(r);
      expect(optionTexts(sourceSelect(r))).toEqual([
        "Output log",
        "Error log",
        "/var/log/docker.log",
        "/var/log/docker/app.log",
        "System log (docker only)",
      ]);
      expect(Array.from(sourceSelect(r).options).map((o) => o.value)).toEqual(SOURCES.map((s) => s.id));
    });

    it("are labelled in German", async () => {
      const r = renderLogs(
        { "GET /services/docker/logs": () => json(answer({ sources: SOURCES, source: "output" })) },
        { locale: "de" },
      );
      await waitFor(() => expect(r.queryByRole("combobox", { name: "Quelle" })).toBeTruthy());
      expect(optionTexts(sourceSelect(r, "Quelle"))).toEqual([
        "Ausgabe-Log",
        "Fehler-Log",
        "/var/log/docker.log",
        "/var/log/docker/app.log",
        "Systemlog (nur docker)",
      ]);
    });

    it("show the id of a kind they do not know, and never the label of the backend", async () => {
      const r = renderLogs({
        "GET /services/docker/logs": () =>
          json(
            answer({
              sources: [{ id: "journal", label: "Journal (nur docker)", path: "", readable: true }],
              source: "journal",
            }),
          ),
      });
      await loaded(r);
      expect(optionTexts(sourceSelect(r))).toEqual(["journal"]);
    });

    it("select the one the server shows", async () => {
      const r = renderLogs({
        "GET /services/docker/logs": () => json(answer({ sources: SOURCES, source: "syslog" })),
      });
      await loaded(r);
      expect(sourceSelect(r).value).toBe("syslog");
    });

    it("that cannot be read are disabled and say that admin mode reads them", async () => {
      const r = renderLogs({
        "GET /services/docker/logs": () =>
          json(answer({ sources: [FILE, { ...SYSLOG, readable: false }], source: FILE.id })),
      });
      await loaded(r);
      const [file, syslog] = Array.from(sourceSelect(r).options);
      expect(file?.disabled).toBe(false);
      expect(syslog?.disabled).toBe(true);
      expect(file?.textContent).toBe("/var/log/docker.log");
      expect(syslog?.textContent).toBe("System log (docker only) – in admin mode");
    });

    it("that cannot be read say it in German", async () => {
      const r = renderLogs(
        {
          "GET /services/docker/logs": () =>
            json(answer({ sources: [FILE, { ...SYSLOG, readable: false }], source: FILE.id })),
        },
        { locale: "de" },
      );
      await waitFor(() => expect(r.queryByRole("combobox", { name: "Quelle" })).toBeTruthy());
      expect(optionTexts(sourceSelect(r, "Quelle"))).toEqual([
        "/var/log/docker.log",
        "Systemlog (nur docker) – im Admin-Modus",
      ]);
    });

    it("show another source's lines when it is chosen: asked for by its id, escaped", async () => {
      const r = renderLogs({
        "GET /services/docker/logs?source=syslog": () =>
          json(answer({ source: "syslog", lines: ["Oct 20 docker[1]: started"], offset: 99 })),
        "GET /services/docker/logs?source=file%3Adocker%2Fapp.log": () =>
          json(answer({ source: "file:docker/app.log", lines: ["app line"], offset: 7 })),
      });
      await loaded(r);
      fireEvent.change(sourceSelect(r), { target: { value: "syslog" } });
      await waitFor(() => expect(shown(r)).toBe("Oct 20 docker[1]: started"));
      expect(sourceSelect(r).value).toBe("syslog");
      expect(r.api.sent("GET", "/services/docker/logs?source=syslog")).toHaveLength(1);
    });

    it("close the stream of the one before and follow the new one from its own offset", async () => {
      const r = renderLogs({
        "GET /services/docker/logs?source=syslog": () =>
          json(answer({ source: "syslog", lines: ["syslog line"], offset: 99 })),
      });
      await loaded(r);
      await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
      stream(0).open();
      fireEvent.change(sourceSelect(r), { target: { value: "syslog" } });
      await waitFor(() => expect(FakeEventSource.instances).toHaveLength(2));
      expect(stream(0).readyState).toBe(FakeEventSource.CLOSED);
      expect(stream(1).readyState).not.toBe(FakeEventSource.CLOSED);
      expect(stream(1).url).toBe("/ext/openrc/api/services/docker/logs/stream?source=syslog&offset=99");
      line(stream(1), "new line");
      await waitFor(() => expect(shown(r)).toBe("syslog line\nnew line"));
    });

    it("show why the source cannot be loaded, with the others still to choose", async () => {
      const r = renderLogs({
        "GET /services/docker/logs?source=syslog": () =>
          apiError(500, "failed", "reading /var/log/messages: boom"),
      });
      await loaded(r);
      fireEvent.change(sourceSelect(r), { target: { value: "syslog" } });
      expect((await r.findByRole("alert")).textContent).toBe("That failed: reading /var/log/messages: boom");
      expect(sourceSelect(r).value).toBe("syslog");
      expect(box(r)).toBeNull();
      // the stream of the source before is closed, and nothing else follows
      expect(stream(0).readyState).toBe(FakeEventSource.CLOSED);
      expect(FakeEventSource.instances).toHaveLength(1);
    });
  });

  describe("when there is nothing to read", () => {
    it("says that the service has no log, and opens no stream", async () => {
      const r = renderLogs({
        "GET /services/docker/logs": () => json(answer({ sources: [], source: "", lines: [], offset: 0 })),
      });
      await r.findByText("docker has no log.");
      expect(r.queryByRole("combobox")).toBeNull();
      expect(FakeEventSource.instances).toEqual([]);
    });

    it("says in German that the service has no log", async () => {
      const r = renderLogs(
        {
          "GET /services/docker/logs": () => json(answer({ sources: [], source: "", lines: [], offset: 0 })),
        },
        { locale: "de" },
      );
      await r.findByText("docker hat kein Log.");
    });

    it("says that admin mode reads the logs when no source can be read, and opens no stream", async () => {
      const r = renderLogs({
        "GET /services/docker/logs": () =>
          json(
            answer({
              sources: [
                { ...FILE, readable: false },
                { ...SYSLOG, readable: false },
              ],
              source: "",
              lines: [],
              offset: 0,
            }),
          ),
      });
      await r.findByText("The logs of docker can only be read in admin mode.");
      const options = Array.from(sourceSelect(r).options);
      expect(options.every((o) => o.disabled)).toBe(true);
      expect(options[0]?.textContent).toBe("/var/log/docker.log – in admin mode");
      expect(FakeEventSource.instances).toEqual([]);
    });

    it("says in German that admin mode reads the logs", async () => {
      const r = renderLogs(
        {
          "GET /services/docker/logs": () =>
            json(answer({ sources: [{ ...FILE, readable: false }], source: "", lines: [], offset: 0 })),
        },
        { locale: "de" },
      );
      await r.findByText("Die Logs von docker kannst du nur im Admin-Modus lesen.");
    });

    it("says that the log is empty", async () => {
      const r = renderLogs({
        "GET /services/docker/logs": () => json(answer({ lines: [], offset: 0 })),
      });
      await loaded(r);
      expect(shown(r)).toBe("No lines yet.");
    });

    it("shows why the log cannot be loaded", async () => {
      const r = renderLogs({
        "GET /services/docker/logs": () => apiError(404, "not_found", "no service docker"),
      });
      expect((await r.findByRole("alert")).textContent).toBe("The service is gone – reload.");
      expect(r.queryByRole("status")).toBeNull();
      expect(FakeEventSource.instances).toEqual([]);
      fireEvent.click(r.getByRole("button", { name: "Close" }));
      expect(r.onClose).toHaveBeenCalledOnce();
    });
  });

  describe("the end of the log", () => {
    // jsdom has no layout: the box is 100 px high and its content as high as 100 px per line
    beforeEach(() => {
      vi.spyOn(Element.prototype, "scrollHeight", "get").mockImplementation(function (this: Element) {
        return this.tagName === "PRE" ? 100 * (this.textContent?.split("\n").length ?? 0) : 0;
      });
      vi.spyOn(Element.prototype, "clientHeight", "get").mockImplementation(function (this: Element) {
        return this.tagName === "PRE" ? 100 : 0;
      });
    });

    afterEach(() => {
      vi.restoreAllMocks();
    });

    it("is where the last lines are shown from", async () => {
      const r = renderLogs();
      await loaded(r);
      await waitFor(() => expect(box(r).scrollTop).toBe(300));
    });

    it("follows the new lines while it is at the end", async () => {
      const r = renderLogs();
      await loaded(r);
      await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
      stream().open();
      line(stream(), "line 4");
      line(stream(), "line 5");
      await waitFor(() => expect(box(r).scrollTop).toBe(500));
    });

    it("stays where it is once the reader scrolled up", async () => {
      const r = renderLogs();
      await loaded(r);
      await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
      await waitFor(() => expect(box(r).scrollTop).toBe(300));
      box(r).scrollTop = 0;
      fireEvent.scroll(box(r));
      stream().open();
      line(stream(), "line 4");
      await waitFor(() => expect(shown(r)).toContain("line 4"));
      expect(box(r).scrollTop).toBe(0);
      // back at the end: it follows again
      box(r).scrollTop = 300; // 300 + 100 = 400 = the height of 4 lines
      fireEvent.scroll(box(r));
      line(stream(), "line 5");
      await waitFor(() => expect(box(r).scrollTop).toBe(500));
    });

    it("is where the lines of another source are shown from", async () => {
      const r = renderLogs({
        "GET /services/docker/logs?source=syslog": () =>
          json(answer({ source: "syslog", lines: ["a", "b", "c", "d", "e"], offset: 5 })),
      });
      await loaded(r);
      await waitFor(() => expect(box(r).scrollTop).toBe(300));
      box(r).scrollTop = 0;
      fireEvent.scroll(box(r));
      fireEvent.change(sourceSelect(r), { target: { value: "syslog" } });
      await waitFor(() => expect(box(r).scrollTop).toBe(500));
    });
  });
});
