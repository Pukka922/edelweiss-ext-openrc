import { FakeEventSource } from "@edelweiss/sdk/testing";
import { cleanup, fireEvent, within } from "@solidjs/testing-library";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { JobState } from "./api";
import { Services } from "./Services";
import { details, find, job, view } from "./test/fixtures";
import { row } from "./test/page";
import {
  adminOnRequest,
  apiError,
  type Handler,
  jobRoutes,
  json,
  renderExt,
  settle,
  stubEventSource,
} from "./test/render";
import { POLL_MS } from "./useJob";

beforeEach(() => {
  vi.useFakeTimers();
  stubEventSource();
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

// The dialogs the page opens by itself: which dialog a click opens, what the page does when it is done, and what the
// page gives them (the job of the view, the way to read the view again, the logs).

/** The page with the routes of a job and the view of the preview's host; more routes win. */
function renderPage(
  opts: {
    kind?: string;
    target?: string;
    states?: Partial<JobState>[];
    routes?: Record<string, Handler>;
    session?: ReturnType<typeof adminOnRequest>;
  } = {},
) {
  const { kind = "stop", target = "docker", states = [{ status: "done" }] } = opts;
  const api = jobRoutes(kind, target, [kind], states, {
    "GET /view": () => json(view()),
    "GET /services/docker": () => json(details("docker")),
    "GET /services/docker/logs": () => json({ sources: [], source: "", lines: [], offset: 0, capped: false }),
    ...opts.routes,
  });
  const r = renderExt(() => <Services />, opts.session);
  return { ...r, api };
}

const click = (r: ReturnType<typeof renderPage>, name: string) =>
  fireEvent.click(r.getByRole("button", { name }));
const confirm = async (r: ReturnType<typeof renderPage>, name: string) => {
  fireEvent.click(within(r.getByRole("dialog")).getByRole("button", { name }));
  await settle();
};

describe("Services: the dialogs of the page", () => {
  it("opens the log view for the logs, without admin mode, and closes it again", async () => {
    const session = adminOnRequest();
    const r = renderPage({ session });
    await settle();
    click(r, "Logs docker");
    await settle();
    expect(r.getByRole("dialog", { name: "Logs: docker" })).toBeTruthy();
    expect(r.api.sent("GET", "/services/docker/logs")).toHaveLength(1);
    expect(session.api.admin()).toBe(false);
    fireEvent.click(r.getByRole("button", { name: "Close" }));
    expect(r.queryByRole("dialog")).toBeNull();
  });

  it("opens the log view of the service of the banner", async () => {
    const r = renderPage({
      routes: {
        "GET /services/nginx/logs": () =>
          json({ sources: [], source: "", lines: [], offset: 0, capped: false }),
      },
    });
    await settle();
    fireEvent.click(within(r.getAllByRole("alert")[0]!).getByRole("button", { name: "Logs nginx" }));
    await settle();
    expect(r.getByRole("dialog", { name: "Logs: nginx" })).toBeTruthy();
  });

  describe("a stop, a start and a restart", () => {
    it("opens the dialog for stop, asks, and when the job is done says so, closes and reads the view again", async () => {
      const r = renderPage({
        routes: { "GET /services/docker": () => json(details("docker", { neededBy: ["nginx"] })) },
      });
      await settle();
      click(r, "Stop docker");
      await settle();
      const d = r.getByRole("dialog", { name: "Stop docker?" });
      expect(within(d).getByText("These stop along with it: nginx")).toBeTruthy();
      await confirm(r, "Stop");
      expect(r.api.sent("POST", "/jobs").map((c) => c.body)).toEqual([{ kind: "stop", service: "docker" }]);
      await vi.advanceTimersByTimeAsync(POLL_MS);
      expect(r.toasts.shown).toEqual([{ kind: "success", message: "docker was stopped." }]);
      expect(r.queryByRole("dialog")).toBeNull();
      expect(r.api.sent("GET", "/view").length).toBeGreaterThanOrEqual(2);
    });

    it("opens the dialog for start", async () => {
      const r = renderPage({ kind: "start", target: "samba" });
      await settle();
      click(r, "Start samba");
      await settle();
      expect(r.getByRole("dialog", { name: "Start samba?" })).toBeTruthy();
      await confirm(r, "Start");
      await vi.advanceTimersByTimeAsync(POLL_MS);
      expect(r.toasts.shown).toEqual([{ kind: "success", message: "samba was started." }]);
    });

    it("opens the dialog for restart, also from the banner of a crashed service", async () => {
      const r = renderPage({
        kind: "restart",
        target: "nginx",
        routes: { "GET /services/nginx": () => json(details("nginx")) },
      });
      await settle();
      fireEvent.click(within(r.getAllByRole("alert")[0]!).getByRole("button", { name: "Restart nginx" }));
      await settle();
      expect(r.getByRole("dialog", { name: "Restart nginx?" })).toBeTruthy();
      await confirm(r, "Restart");
      expect(r.api.sent("POST", "/jobs").map((c) => c.body)).toEqual([{ kind: "restart", service: "nginx" }]);
    });

    it("opens the dialog of the warning for sshd", async () => {
      const r = renderPage({ target: "sshd", routes: { "GET /services/sshd": () => json(details("sshd")) } });
      await settle();
      click(r, "Stop sshd");
      await settle();
      const d = r.getByRole("dialog", { name: "Stop sshd?" });
      expect(within(d).getByRole("checkbox", { name: "I know what I am doing" })).toBeTruthy();
    });

    it("judges what stops along by the view of the page", async () => {
      // the view says that nginx runs (crashed) and samba does not
      const r = renderPage({
        routes: { "GET /services/docker": () => json(details("docker", { neededBy: ["nginx", "samba"] })) },
      });
      await settle();
      click(r, "Stop docker");
      await settle();
      expect(r.getByText("These stop along with it: nginx")).toBeTruthy();
    });
  });

  it("opens the restart of edelweiss itself instead of the dialog of a restart", async () => {
    const r = renderPage({ kind: "restart", target: "edelweiss" });
    await settle();
    click(r, "Restart edelweiss");
    await settle();
    const d = r.getByRole("dialog", { name: "Restart edelweiss?" });
    expect(
      within(d).getByText("The page is gone for a few seconds; after that you have to log in again."),
    ).toBeTruthy();
    // the relations of edelweiss are not read: its restart ends the session whatever depends on it
    expect(r.api.sent("GET", "/services/edelweiss")).toHaveLength(0);
  });

  describe("the autostart", () => {
    it("opens the dialog for the switch on, and says when it is done", async () => {
      const r = renderPage({ kind: "enable", target: "samba" });
      await settle();
      fireEvent.click(r.getByRole("switch", { name: "Autostart samba" }));
      await settle();
      expect(r.getByRole("dialog", { name: "Autostart: samba" })).toBeTruthy();
      expect(r.getByRole("combobox", { name: "Runlevel" })).toBeTruthy();
      await confirm(r, "Apply");
      expect(r.api.sent("POST", "/jobs").map((c) => c.body)).toEqual([
        { kind: "enable", service: "samba", runlevel: "default" },
      ]);
      await vi.advanceTimersByTimeAsync(POLL_MS);
      expect(r.toasts.shown).toEqual([{ kind: "success", message: "The autostart of samba is on." }]);
      expect(r.queryByRole("dialog")).toBeNull();
    });

    it("opens the dialog for the switch off", async () => {
      const r = renderPage({ kind: "disable", target: "docker" });
      await settle();
      fireEvent.click(r.getByRole("switch", { name: "Autostart docker" }));
      await settle();
      expect(r.getByText("docker will be taken out of the autostart (default).")).toBeTruthy();
      await confirm(r, "Apply");
      expect(r.api.sent("POST", "/jobs").map((c) => c.body)).toEqual([
        { kind: "disable", service: "docker", runlevel: "default" },
      ]);
    });
  });

  describe("a job that fails", () => {
    const FAILED: Partial<JobState> = {
      status: "failed",
      code: "failed",
      error: "rc-service samba start: exit status 1",
      output: " * Failed to start samba  [ !! ]",
    };

    it("shows the output, and View log opens the logs of that service in place of the dialog", async () => {
      const r = renderPage({
        kind: "start",
        target: "samba",
        states: [FAILED],
        routes: {
          "GET /services/samba/logs": () =>
            json({
              sources: [{ id: "file:samba.log", label: "x", path: "/var/log/samba.log", readable: true }],
              source: "file:samba.log",
              lines: ["samba log line"],
              offset: 15,
              capped: false,
            }),
        },
      });
      await settle();
      click(r, "Start samba");
      await settle();
      await confirm(r, "Start");
      await vi.advanceTimersByTimeAsync(POLL_MS);
      const d = r.getByRole("dialog", { name: "samba could not be started" });
      expect(d.querySelector("pre")?.textContent).toBe(" * Failed to start samba  [ !! ]");
      expect(r.toasts.shown).toEqual([]);
      fireEvent.click(within(d).getByRole("button", { name: "View log" }));
      await settle();
      expect(r.getAllByRole("dialog")).toHaveLength(1);
      const logs = r.getByRole("dialog", { name: "Logs: samba" });
      expect(logs.querySelector("pre")?.textContent).toBe("samba log line");
      expect(r.api.sent("GET", "/services/samba/logs")).toHaveLength(1);
      expect(FakeEventSource.instances).toHaveLength(1);
    });
  });

  describe("what the page gives the dialog", () => {
    it("lets the dialog fall back to the job of the view when the job cannot be asked for", async () => {
      // the view has the job J9 as interrupted: the dialog, whose poll gets a 404, says so
      const seen = job({
        id: "J9",
        kind: "stop",
        target: "docker",
        status: "interrupted",
        code: "interrupted",
        steps: ["stop"],
      });
      const r = renderPage({
        states: [{}],
        routes: {
          "GET /view": () => json(view({ job: seen })),
          "GET /jobs/J9": () => apiError(404, "not_found", "there is no such job"),
        },
      });
      await settle();
      click(r, "Stop docker");
      await settle();
      await confirm(r, "Stop");
      await vi.advanceTimersByTimeAsync(POLL_MS);
      const d = r.getByRole("dialog", { name: "Stop docker?" });
      expect(within(d).getByRole("alert").textContent).toContain("The job was interrupted at step 1/1");
      expect(within(d).queryByText("Status unknown – reload")).toBeNull();
    });

    it.each([
      [400, "invalid_input", { field: "kind" }],
      [404, "not_found", {}],
    ])("reads the view again when the dialog is refused with %i %s", async (status, code, extra) => {
      const r = renderPage({
        routes: { "POST /jobs": () => apiError(status, code, "the message", extra) },
      });
      await settle();
      click(r, "Stop docker");
      await settle();
      expect(r.api.sent("GET", "/view")).toHaveLength(1);
      await confirm(r, "Stop");
      expect(r.api.sent("GET", "/view")).toHaveLength(2);
      expect(r.getByRole("dialog", { name: "Stop docker?" })).toBeTruthy();
    });

    it("does not read the view again for a refusal that is no sign of an old view", async () => {
      const r = renderPage({ routes: { "POST /jobs": () => apiError(409, "busy", "busy") } });
      await settle();
      click(r, "Stop docker");
      await settle();
      await confirm(r, "Stop");
      expect(r.api.sent("GET", "/view")).toHaveLength(1);
    });

    it("gives the dialog the service as the view read after admin mode has it", async () => {
      // in admin mode the process reads more: docker turns out not to run, so a stop no longer fits
      let reads = 0;
      const session = adminOnRequest();
      const r = renderPage({
        session,
        routes: {
          "GET /view": () => {
            const v = view();
            if (reads++ > 0) find(v, "docker").state = "stopped";
            return json(v);
          },
        },
      });
      await settle();
      click(r, "Stop docker");
      await vi.advanceTimersByTimeAsync(10);
      await settle();
      const d = r.getByRole("dialog", { name: "Stop docker?" });
      expect(within(d).getByRole("alert").textContent).toBe(
        "The service has changed in the meantime. Reload the list and try again.",
      );
      expect(r.api.sent("POST", "/jobs")).toHaveLength(0);
      expect(r.api.sent("GET", "/services/docker")).toHaveLength(0);
    });
  });

  it("keeps its dialog when the view is read again, and the dialog goes on while the page reads", async () => {
    const r = renderPage({ states: [{}] });
    await settle();
    click(r, "Stop docker");
    await settle();
    const d = r.getByRole("dialog", { name: "Stop docker?" });
    await confirm(r, "Stop");
    await vi.advanceTimersByTimeAsync(5000);
    expect(r.api.sent("GET", "/view").length).toBeGreaterThan(1);
    expect(r.getByRole("dialog", { name: "Stop docker?" })).toBe(d);
    expect(within(d).getByText("Step 1/1: stop the service")).toBeTruthy();
  });

  it("shows the job that the view has running for the service of an open dialog alike in the dialog, the row and the banner", async () => {
    // the dialog sends the stop of docker; the view that the page reads after it has that job running
    const running = job({ id: "J9", kind: "stop", target: "docker", steps: ["stop"], step: 0 });
    let sent = false;
    const r = renderPage({
      states: [{}],
      routes: {
        "POST /jobs": () => {
          sent = true;
          return json(running, 202);
        },
        "GET /view": () => json(view(sent ? { job: running } : {})),
      },
    });
    await settle();
    click(r, "Stop docker");
    await settle();
    // before: no job in the view, the row has its buttons, no banner
    expect(r.queryByText("Running: Stop docker")).toBeNull();
    expect(within(row(r, "docker")).getByRole("button", { name: "Restart docker" })).toBeTruthy();
    await confirm(r, "Stop");
    await vi.advanceTimersByTimeAsync(5000);
    const step = "Step 1/1: stop the service";
    const d = r.getByRole("dialog", { name: "Stop docker?" });
    // the dialog tells the step of the job, and the banner of the page the same job with the same step
    expect(within(d).getByText(step)).toBeTruthy();
    expect(r.getByText("Running: Stop docker")).toBeTruthy();
    expect(r.getAllByText(step)).toHaveLength(2);
    // the row of its service has the spinner in place of its buttons, and a row of another service keeps them
    const docker = row(r, "docker");
    expect(within(docker).getByRole("status", { name: "Change in progress …" })).toBeTruthy();
    expect(within(docker).queryByRole("button", { name: "Restart docker" })).toBeNull();
    expect(within(docker).queryByRole("button", { name: "Stop docker" })).toBeNull();
    expect(within(row(r, "chronyd")).queryByRole("status")).toBeNull();
    expect(within(row(r, "chronyd")).getByRole("button", { name: "Stop chronyd" })).toBeTruthy();
  });
});
