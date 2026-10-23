import { cleanup, fireEvent, within } from "@solidjs/testing-library";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ServiceView } from "../api";
import { button, DONE, dialog, renderAction, routes } from "../test/dialog";
import { details, docker, find, samba, service, view } from "../test/fixtures";
import { apiError, jobRoutes, json, settle, stubApi } from "../test/render";
import { POLL_MS } from "../useJob";
import type { ActionKind } from "./ActionDialog";

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("ActionDialog: start", () => {
  it("asks before it starts the service, and sends the request without a confirmation", async () => {
    const api = routes("start", samba(), DONE);
    const r = renderAction("start", samba());
    const d = dialog(r, "Start samba?");
    expect(within(d).getByText("samba will be started.")).toBeTruthy();
    expect(button(r, "Start").className).toContain("btn-primary");
    fireEvent.click(button(r, "Start"));
    await settle();
    expect(api.sent("POST", "/jobs").map((c) => c.body)).toEqual([{ kind: "start", service: "samba" }]);
    // it needs nobody that stops along: the relations are not read
    expect(api.sent("GET", "/services/samba")).toHaveLength(0);
  });

  it("shows the step while the job runs, with the buttons off, and is done with the text of the job", async () => {
    routes("start", samba(), [{}, { status: "done" }]);
    const r = renderAction("start", samba());
    fireEvent.click(button(r, "Start"));
    await settle();
    expect(r.getByText("Step 1/1: start the service")).toBeTruthy();
    expect(button(r, "Cancel").disabled).toBe(true);
    expect(button(r, "Start").disabled).toBe(true);
    expect(r.onDone).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(POLL_MS);
    await vi.advanceTimersByTimeAsync(POLL_MS);
    expect(r.onDone).toHaveBeenCalledExactlyOnceWith("samba was started.");
  });

  it("speaks German", async () => {
    routes("start", samba(), DONE);
    const r = renderAction("start", samba(), { locale: "de" });
    const d = dialog(r, "samba starten?");
    expect(within(d).getByText("samba wird gestartet.")).toBeTruthy();
    expect(button(r, "Abbrechen")).toBeTruthy();
    fireEvent.click(button(r, "Starten"));
    await settle();
    await vi.advanceTimersByTimeAsync(POLL_MS);
    expect(r.onDone).toHaveBeenCalledExactlyOnceWith("samba wurde gestartet.");
  });

  it("starts a service whose start failed", async () => {
    const redis = find(view(), "redis");
    const api = routes("start", redis, DONE);
    const r = renderAction("start", redis);
    fireEvent.click(button(r, "Start"));
    await settle();
    expect(api.sent("POST", "/jobs").map((c) => c.body)).toEqual([{ kind: "start", service: "redis" }]);
  });

  it("closes with Cancel, and with Escape", () => {
    routes("start", samba(), DONE);
    const r = renderAction("start", samba());
    fireEvent.click(button(r, "Cancel"));
    expect(r.onClose).toHaveBeenCalledOnce();
    fireEvent.keyDown(document, { key: "Escape" });
    expect(r.onClose).toHaveBeenCalledTimes(2);
  });
});

describe("ActionDialog: a plain stop or restart", () => {
  // the relations of docker: cronie (starting), net.eth0 (inactive), networking (started, no daemon) and nginx (crashed)
  // are up and go down with it; redis (its start failed), samba (stopped) and rsyslog (stopping) are not; ghost is no
  // service of the view
  const NEEDED_BY = ["cronie", "ghost", "net.eth0", "networking", "nginx", "redis", "rsyslog", "samba"];

  it("names the services that need it and are up, from the relations of the service", async () => {
    const api = routes("stop", docker(), DONE, NEEDED_BY);
    const r = renderAction("stop", docker());
    const d = dialog(r, "Stop docker?");
    expect(within(d).getByText("docker will be stopped.")).toBeTruthy();
    await settle();
    expect(api.sent("GET", "/services/docker")).toHaveLength(1);
    expect(within(d).getByText("These stop along with it: cronie, net.eth0, networking, nginx")).toBeTruthy();
    expect(d.querySelector('[role="alert"]')).toBeNull(); // a plain confirm: no red callout
    expect(r.queryByRole("checkbox")).toBeNull();
  });

  it("names them for a restart as well, and in German", async () => {
    routes("restart", docker(), DONE, NEEDED_BY);
    const r = renderAction("restart", docker(), { locale: "de" });
    const d = dialog(r, "docker neu starten?");
    expect(within(d).getByText("docker wird neu gestartet.")).toBeTruthy();
    await settle();
    expect(
      within(d).getByText("Dabei werden auch gestoppt: cronie, net.eth0, networking, nginx"),
    ).toBeTruthy();
  });

  it.each([
    ["nothing needs it", []],
    ["what needs it is not up", ["redis", "samba"]],
  ])("names none when %s", async (_what, neededBy) => {
    routes("stop", docker(), DONE, neededBy);
    const r = renderAction("stop", docker());
    await settle();
    expect(r.queryByText(/stop along/)).toBeNull();
    expect(button(r, "Stop").disabled).toBe(false);
  });

  it("judges by the view it was given: a service that is up there goes down with it", async () => {
    const list = view().services.map((s) => (s.name === "samba" ? { ...s, state: "started" as const } : s));
    routes("stop", docker(), DONE, ["nginx", "samba"]);
    const r = renderAction("stop", docker(), { services: list });
    await settle();
    expect(r.getByText("These stop along with it: nginx, samba")).toBeTruthy();
  });

  it("sends the request without a confirmation once the relations are there, and not before", async () => {
    const api = routes("stop", docker(), DONE, ["nginx"]);
    const r = renderAction("stop", docker());
    expect(button(r, "Stop").disabled).toBe(true);
    fireEvent.click(button(r, "Stop"));
    expect(api.sent("POST", "/jobs")).toHaveLength(0);
    await settle();
    expect(button(r, "Stop").disabled).toBe(false);
    expect(button(r, "Stop").className).toContain("btn-primary");
    fireEvent.click(button(r, "Stop"));
    await settle();
    expect(api.sent("POST", "/jobs").map((c) => c.body)).toEqual([{ kind: "stop", service: "docker" }]);
    await vi.advanceTimersByTimeAsync(POLL_MS);
    expect(r.onDone).toHaveBeenCalledExactlyOnceWith("docker was stopped.");
  });

  it("is done with the text of the restart", async () => {
    routes("restart", docker(), DONE);
    const r = renderAction("restart", docker());
    await settle();
    fireEvent.click(button(r, "Restart"));
    await settle();
    await vi.advanceTimersByTimeAsync(POLL_MS);
    expect(r.onDone).toHaveBeenCalledExactlyOnceWith("docker was restarted.");
  });

  it("escapes the name once when it reads the relations", async () => {
    const odd = service({ name: "a+b" });
    const api = stubApi({
      "GET /services/a%2Bb": () => json(details("docker", { name: "a+b", neededBy: [] })),
    });
    renderAction("stop", odd);
    await settle();
    expect(api.sent("GET", "/services/a%2Bb")).toHaveLength(1);
  });

  it("keeps the list while the job runs, and reads the relations once", async () => {
    const api = routes("stop", docker(), [{}, { status: "done" }], ["nginx"]);
    const r = renderAction("stop", docker());
    await settle();
    fireEvent.click(button(r, "Stop"));
    await settle();
    await vi.advanceTimersByTimeAsync(POLL_MS);
    expect(r.getByText("Step 1/1: stop the service")).toBeTruthy();
    expect(r.getByText("These stop along with it: nginx")).toBeTruthy();
    expect(api.sent("GET", "/services/docker")).toHaveLength(1);
  });
});

describe("ActionDialog: a request that is refused", () => {
  const refusing = (status: number, code: string, details: Record<string, unknown> = {}) =>
    stubApi({
      "GET /services/docker": () => json(details_("docker")),
      "POST /jobs": () => apiError(status, code, `the message of ${code}`, details),
    });
  const details_ = (name: string) => details(name, { neededBy: [] });

  it("shows why in the text of the code, and can try again", async () => {
    const api = refusing(409, "busy");
    const r = renderAction("stop", docker());
    await settle();
    fireEvent.click(button(r, "Stop"));
    await settle();
    expect(r.getByRole("alert").textContent).toBe("Another change is running – try again in a moment.");
    expect(r.onReload).not.toHaveBeenCalled();
    expect(button(r, "Stop").disabled).toBe(false);
    fireEvent.click(button(r, "Stop"));
    await settle();
    expect(api.sent("POST", "/jobs")).toHaveLength(2);
  });

  it("names the service that protects it", async () => {
    refusing(409, "protected", { reason: "base", service: "devfs" });
    const r = renderAction("stop", docker());
    await settle();
    fireEvent.click(button(r, "Stop"));
    await settle();
    expect(r.getByRole("alert").textContent).toBe(
      "devfs is protected (system base) and cannot be changed that way.",
    );
    expect(r.onReload).not.toHaveBeenCalled();
  });

  it.each([
    [
      400,
      "invalid_input",
      { field: "kind" },
      "The service has changed in the meantime. Reload the list and try again.",
    ],
    [404, "not_found", {}, "The service is gone – reload."],
  ])(
    "reads the view again after %s %s, shows the text and leaves only Close",
    async (status, code, extra, text) => {
      const api = refusing(status, code, extra);
      const r = renderAction("stop", docker());
      await settle();
      fireEvent.click(button(r, "Stop"));
      await settle();
      expect(r.onReload).toHaveBeenCalledOnce();
      expect(r.getByRole("alert").textContent).toBe(text);
      expect(r.onClose).not.toHaveBeenCalled();
      // a second click would send a request that is refused again
      expect(r.queryByRole("button", { name: "Stop" })).toBeNull();
      expect(r.queryByRole("button", { name: "Cancel" })).toBeNull();
      expect(r.queryByRole("button", { name: "View log" })).toBeNull();
      fireEvent.click(button(r, "Close"));
      expect(r.onClose).toHaveBeenCalledOnce();
      expect(api.sent("POST", "/jobs")).toHaveLength(1);
    },
  );

  it("leaves only Close in German, too", async () => {
    refusing(404, "not_found");
    const r = renderAction("stop", docker(), { locale: "de" });
    await settle();
    fireEvent.click(button(r, "Stoppen"));
    await settle();
    expect(r.queryByRole("button", { name: "Stoppen" })).toBeNull();
    expect(button(r, "Schließen")).toBeTruthy();
  });

  it("leaves only Close for a job whose state is not known, which could be sent twice otherwise", async () => {
    // the poll gets a 404 and the view of the page has no such job
    jobRoutes("stop", "docker", ["stop"], [{}], {
      "GET /services/docker": () => json(details("docker")),
      "GET /jobs/J9": () => apiError(404, "not_found", "there is no such job"),
    });
    const r = renderAction("stop", docker());
    await settle();
    fireEvent.click(button(r, "Stop"));
    await settle();
    await vi.advanceTimersByTimeAsync(POLL_MS);
    expect(r.getByRole("status").textContent).toBe("Status unknown – reload");
    expect(r.queryByRole("button", { name: "Stop" })).toBeNull();
    expect(button(r, "Close")).toBeTruthy();
  });

  it("does not read the view again for a job that failed", async () => {
    routes("stop", docker(), [{ status: "failed", code: "failed", error: "boom" }]);
    const r = renderAction("stop", docker());
    await settle();
    fireEvent.click(button(r, "Stop"));
    await settle();
    await vi.advanceTimersByTimeAsync(POLL_MS);
    expect(r.onReload).not.toHaveBeenCalled();
  });
});

describe("ActionDialog: a kind that no longer fits the service", () => {
  const CASES: [string, ActionKind, ServiceView][] = [
    ["a start of a service that is started", "start", find(view(), "docker")],
    ["a start of a service that is starting", "start", find(view(), "cronie")],
    ["a stop of a service that is stopped", "stop", find(view(), "samba")],
    ["a stop of a service that is stopping", "stop", find(view(), "rsyslog")],
    ["a stop of a service whose start failed", "stop", find(view(), "redis")],
  ];

  it.each(CASES)(
    "says that the service changed, and sends and reads nothing, for %s",
    async (_what, kind, s) => {
      const api = routes(kind, s, DONE, ["nginx"]);
      const r = renderAction(kind, s);
      await settle();
      const text = "The service has changed in the meantime. Reload the list and try again.";
      expect(r.getByRole("alert").textContent).toBe(text);
      // nothing is left that sends
      expect(r.queryByRole("button", { name: "Start" })).toBeNull();
      expect(r.queryByRole("button", { name: "Stop" })).toBeNull();
      expect(r.queryByRole("checkbox")).toBeNull();
      expect(api.calls).toEqual([]);
      fireEvent.click(r.getByRole("button", { name: "Close" }));
      expect(r.onClose).toHaveBeenCalledOnce();
      expect(api.calls).toEqual([]);
    },
  );

  it("says it in German", async () => {
    const s = find(view(), "samba");
    routes("stop", s, DONE);
    const r = renderAction("stop", s, { locale: "de" });
    expect(r.getByRole("alert").textContent).toBe(
      "Der Dienst hat sich inzwischen geändert. Lade die Liste neu und versuche es noch einmal.",
    );
    expect(r.getByRole("button", { name: "Schließen" })).toBeTruthy();
  });

  it("still restarts a service in any state, and starts one that crashed", async () => {
    for (const [kind, name] of [
      ["restart", "samba"],
      ["restart", "redis"],
      ["start", "nginx"],
    ] as const) {
      const s = find(view(), name);
      const api = routes(kind, s, DONE);
      const r = renderAction(kind, s);
      await settle();
      expect(r.queryByText(/changed in the meantime/), `${kind} ${name}`).toBeNull();
      fireEvent.click(button(r, { start: "Start", restart: "Restart" }[kind]));
      await settle();
      expect(api.sent("POST", "/jobs"), `${kind} ${name}`).toHaveLength(1);
      cleanup();
    }
  });
});

describe("ActionDialog: values that hold a $", () => {
  it("shows the name as it is", async () => {
    const odd = service({ name: "a$&b" });
    stubApi({ "GET /services/a%24%26b": () => json(details("docker", { name: "a$&b", neededBy: [] })) });
    const r = renderAction("stop", odd);
    expect(r.getByRole("dialog", { name: "Stop a$&b?" })).toBeTruthy();
    expect(r.getByText("a$&b will be stopped.")).toBeTruthy();
  });
});
