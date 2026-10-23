import { cleanup, fireEvent, within } from "@solidjs/testing-library";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { JobState, ServiceView } from "../api";
import { button, levelSelect, renderAutostart, routes } from "../test/dialog";
import { docker, find, nftables, samba, service, sshd, view } from "../test/fixtures";
import { apiError, json, settle, stubApi } from "../test/render";
import { POLL_MS } from "../useJob";
import type { AutostartKind } from "./AutostartDialog";

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

const optionTexts = (el: HTMLSelectElement) =>
  Array.from(el.options).map((o) => `${o.value}: ${o.textContent}`);

describe("AutostartDialog: switching it on", () => {
  it("offers default (preselected) and boot, with the note that nothing starts or stops now", () => {
    routes("enable", samba());
    const r = renderAutostart("enable", samba());
    const d = r.getByRole("dialog", { name: "Autostart: samba" });
    expect(optionTexts(levelSelect(r))).toEqual([
      "default: default (normal)",
      "boot: boot (early at startup)",
    ]);
    expect(levelSelect(r).value).toBe("default");
    expect(
      within(d).getByText(
        "Almost all services belong in default. Changing this does not start or stop the service right away.",
      ),
    ).toBeTruthy();
    expect(r.queryByRole("checkbox")).toBeNull();
    expect(button(r, "Apply").className).toContain("btn-primary");
  });

  it("speaks German, in the words of the preview", () => {
    routes("enable", samba());
    const r = renderAutostart("enable", samba(), "de");
    const d = r.getByRole("dialog", { name: "Autostart: samba" });
    const level = r.getByRole("combobox", { name: "Runlevel" }) as HTMLSelectElement;
    expect(optionTexts(level)).toEqual(["default: default (normal)", "boot: boot (früh beim Start)"]);
    expect(
      within(d).getByText(
        "Fast alle Dienste gehören in default. Das Ändern startet oder stoppt den Dienst nicht sofort.",
      ),
    ).toBeTruthy();
    expect(button(r, "Abbrechen")).toBeTruthy();
    expect(button(r, "Übernehmen")).toBeTruthy();
  });

  it("sends enable with default, reads nothing else, and is done with the text of the job", async () => {
    const api = routes("enable", samba());
    const r = renderAutostart("enable", samba());
    fireEvent.click(button(r, "Apply"));
    await settle();
    expect(api.sent("POST", "/jobs").map((c) => c.body)).toEqual([
      { kind: "enable", service: "samba", runlevel: "default" },
    ]);
    expect(api.calls.filter((c) => c.method === "GET")).toEqual([]);
    expect(r.getByText("Step 1/1: add it to the autostart")).toBeTruthy();
    await vi.advanceTimersByTimeAsync(POLL_MS);
    expect(r.onDone).toHaveBeenCalledExactlyOnceWith("The autostart of samba is on.");
  });

  it("sends the runlevel that is chosen", async () => {
    const api = routes("enable", samba());
    const r = renderAutostart("enable", samba(), "de");
    fireEvent.change(r.getByRole("combobox", { name: "Runlevel" }), { target: { value: "boot" } });
    fireEvent.click(button(r, "Übernehmen"));
    await settle();
    expect(api.sent("POST", "/jobs").map((c) => c.body)).toEqual([
      { kind: "enable", service: "samba", runlevel: "boot" },
    ]);
    await vi.advanceTimersByTimeAsync(POLL_MS);
    expect(r.onDone).toHaveBeenCalledExactlyOnceWith("Der Autostart von samba ist eingeschaltet.");
  });

  it("puts a service in boot or default that is in another runlevel only", async () => {
    const odd = service({ name: "odd", runlevels: ["nonetwork"], state: "stopped" });
    const api = routes("enable", odd);
    const r = renderAutostart("enable", odd);
    fireEvent.click(button(r, "Apply"));
    await settle();
    expect(api.sent("POST", "/jobs").map((c) => c.body)).toEqual([
      { kind: "enable", service: "odd", runlevel: "default" },
    ]);
  });

  it("has the buttons off while the job runs, and closes with Cancel", async () => {
    routes("enable", samba(), [{}]);
    const r = renderAutostart("enable", samba());
    fireEvent.click(button(r, "Cancel"));
    expect(r.onClose).toHaveBeenCalledOnce();
    fireEvent.click(button(r, "Apply"));
    await settle();
    expect(button(r, "Apply").disabled).toBe(true);
    expect(button(r, "Cancel").disabled).toBe(true);
    expect(levelSelect(r).disabled).toBe(true);
  });
});

describe("AutostartDialog: switching it off", () => {
  it("asks for a confirmation that names the runlevel, with no select", async () => {
    const api = routes("disable", docker());
    const r = renderAutostart("disable", docker());
    const d = r.getByRole("dialog", { name: "Autostart: docker" });
    expect(within(d).getByText("docker will be taken out of the autostart (default).")).toBeTruthy();
    expect(within(d).getByText("Changing this does not start or stop the service right away.")).toBeTruthy();
    expect(r.queryByRole("combobox")).toBeNull();
    expect(r.queryByRole("alert")).toBeNull();
    fireEvent.click(button(r, "Apply"));
    await settle();
    expect(api.sent("POST", "/jobs").map((c) => c.body)).toEqual([
      { kind: "disable", service: "docker", runlevel: "default" },
    ]);
    await vi.advanceTimersByTimeAsync(POLL_MS);
    expect(r.onDone).toHaveBeenCalledExactlyOnceWith("The autostart of docker is off.");
  });

  it("speaks German", async () => {
    routes("disable", docker());
    const r = renderAutostart("disable", docker(), "de");
    expect(r.getByText("docker wird aus dem Autostart genommen (default).")).toBeTruthy();
    expect(r.getByText("Das Ändern startet oder stoppt den Dienst nicht sofort.")).toBeTruthy();
    fireEvent.click(button(r, "Übernehmen"));
    await settle();
    await vi.advanceTimersByTimeAsync(POLL_MS);
    expect(r.onDone).toHaveBeenCalledExactlyOnceWith("Der Autostart von docker ist ausgeschaltet.");
  });

  it("sends the runlevel the service is in when it is in one", async () => {
    const early = service({ name: "early", runlevels: ["boot"] });
    const api = routes("disable", early);
    const r = renderAutostart("disable", early);
    expect(r.getByText("early will be taken out of the autostart (boot).")).toBeTruthy();
    fireEvent.click(button(r, "Apply"));
    await settle();
    expect(api.sent("POST", "/jobs").map((c) => c.body)).toEqual([
      { kind: "disable", service: "early", runlevel: "boot" },
    ]);
  });

  it("sends no runlevel for a service that is in both default and boot", async () => {
    const both = service({ name: "both", runlevels: ["boot", "default"] });
    const api = routes("disable", both);
    const r = renderAutostart("disable", both);
    expect(r.getByText("both will be taken out of the autostart (boot, default).")).toBeTruthy();
    fireEvent.click(button(r, "Apply"));
    await settle();
    expect(api.sent("POST", "/jobs").map((c) => c.body)).toEqual([{ kind: "disable", service: "both" }]);
    expect(Object.keys(api.sent("POST", "/jobs")[0]?.body as object)).toEqual(["kind", "service"]);
  });

  it("sends only a runlevel of the autostart: another one it is in stays out of it", async () => {
    const odd = service({ name: "odd", runlevels: ["nonetwork", "default"] });
    const api = routes("disable", odd);
    const r = renderAutostart("disable", odd);
    expect(r.getByText("odd will be taken out of the autostart (default).")).toBeTruthy();
    fireEvent.click(button(r, "Apply"));
    await settle();
    expect(api.sent("POST", "/jobs").map((c) => c.body)).toEqual([
      { kind: "disable", service: "odd", runlevel: "default" },
    ]);
  });
});

describe("AutostartDialog: a class with a warning", () => {
  it("shows the red callout and the check for the switch off of sshd, and sends confirm: true", async () => {
    const api = routes("disable", sshd());
    const r = renderAutostart("disable", sshd());
    expect(r.getByRole("alert").textContent).toBe(
      "sshd is your remote access. Without sshd you can no longer reach localhost over SSH. The web interface stays available.",
    );
    expect(button(r, "Apply").className).toContain("btn-error");
    expect(button(r, "Apply").disabled).toBe(true);
    fireEvent.click(button(r, "Apply"));
    await settle();
    expect(api.sent("POST", "/jobs")).toHaveLength(0);
    fireEvent.click(r.getByRole("checkbox", { name: "I know what I am doing" }));
    fireEvent.click(button(r, "Apply"));
    await settle();
    expect(api.sent("POST", "/jobs").map((c) => c.body)).toEqual([
      { kind: "disable", service: "sshd", runlevel: "default", confirm: true },
    ]);
    await vi.advanceTimersByTimeAsync(POLL_MS);
    expect(r.onDone).toHaveBeenCalledExactlyOnceWith("The autostart of sshd is off.");
  });

  it("speaks German", () => {
    routes("disable", sshd());
    const r = renderAutostart("disable", sshd(), "de");
    expect(r.getByRole("alert").textContent).toContain(
      "Ohne sshd kommst du nicht mehr per SSH auf localhost.",
    );
    expect(r.getByRole("checkbox", { name: "Ich weiß, was ich tue" })).toBeTruthy();
  });

  it("warns for the switch on of a firewall as well, with the select of the runlevel", async () => {
    const api = routes("enable", nftables());
    const r = renderAutostart("enable", nftables());
    expect(r.getByRole("alert").textContent).toBe("New firewall rules can lock you out.");
    expect(levelSelect(r)).toBeTruthy();
    fireEvent.change(levelSelect(r), { target: { value: "boot" } });
    fireEvent.click(r.getByRole("checkbox"));
    fireEvent.click(button(r, "Apply"));
    await settle();
    expect(api.sent("POST", "/jobs").map((c) => c.body)).toEqual([
      { kind: "enable", service: "nftables", runlevel: "boot", confirm: true },
    ]);
  });

  it("has no callout and no check where the policy allows it", () => {
    routes("enable", samba());
    const r = renderAutostart("enable", samba());
    expect(r.queryByRole("alert")).toBeNull();
    expect(r.queryByRole("checkbox")).toBeNull();
  });

  it("shows the warning the server asks for although the view did not say so", async () => {
    let posts = 0;
    const api = stubApi({
      "POST /jobs": () =>
        ++posts === 1
          ? apiError(409, "confirm_required", "needs a confirmation", { reason: "storage", services: [] })
          : json(
              {
                id: "J9",
                kind: "disable",
                target: "docker",
                status: "done",
                steps: ["disable"],
                step: 0,
                started: "2026-10-20T12:00:00Z",
              },
              202,
            ),
    });
    const r = renderAutostart("disable", docker());
    fireEvent.click(button(r, "Apply"));
    await settle();
    expect(r.getByRole("alert").textContent).toBe("File systems are unmounted, RAID arrays are stopped.");
    expect(button(r, "Apply").disabled).toBe(true);
    fireEvent.click(r.getByRole("checkbox"));
    fireEvent.click(button(r, "Apply"));
    await settle();
    expect(api.sent("POST", "/jobs").map((c) => c.body)).toEqual([
      { kind: "disable", service: "docker", runlevel: "default" },
      { kind: "disable", service: "docker", runlevel: "default", confirm: true },
    ]);
    expect(r.onDone).toHaveBeenCalledOnce();
  });
});

describe("AutostartDialog: a job that fails", () => {
  const FAILED: Partial<JobState> = {
    status: "failed",
    code: "failed",
    error: "rc-update add samba default: exit status 1",
    output: "rc-update: service `samba' does not exist",
  };

  it("shows the output in a pre, and View log and Close", async () => {
    routes("enable", samba(), [FAILED]);
    const r = renderAutostart("enable", samba());
    fireEvent.click(button(r, "Apply"));
    await settle();
    await vi.advanceTimersByTimeAsync(POLL_MS);
    const d = r.getByRole("dialog", { name: "The autostart of samba could not be changed" });
    expect(d.querySelector("pre")?.textContent).toBe(FAILED.output);
    expect(r.queryByRole("button", { name: "Apply" })).toBeNull();
    fireEvent.click(button(r, "View log"));
    expect(r.onLogs).toHaveBeenCalledOnce();
    fireEvent.click(button(r, "Close"));
    expect(r.onClose).toHaveBeenCalledOnce();
    expect(r.onDone).not.toHaveBeenCalled();
  });

  it("is titled in German", async () => {
    routes("disable", docker(), [FAILED]);
    const r = renderAutostart("disable", docker(), "de");
    fireEvent.click(button(r, "Übernehmen"));
    await settle();
    await vi.advanceTimersByTimeAsync(POLL_MS);
    expect(r.getByRole("dialog", { name: "Der Autostart von docker ließ sich nicht ändern" })).toBeTruthy();
    expect(button(r, "Log ansehen")).toBeTruthy();
  });
});

describe("AutostartDialog: a job that ends without output", () => {
  it.each([
    ["interrupted", { status: "interrupted", code: "interrupted" }, "The job was interrupted at step 1/1"],
    ["failed", { status: "failed", code: "failed", error: "exit status 1" }, "failed: exit status 1"],
  ] as const)("shows a job that %s as the alert, and leaves only Close", async (_what, state, text) => {
    routes("enable", samba(), [state]);
    const r = renderAutostart("enable", samba());
    fireEvent.click(button(r, "Apply"));
    await settle();
    await vi.advanceTimersByTimeAsync(POLL_MS);
    expect(r.getByRole("alert").textContent).toContain(text);
    expect(r.queryByRole("button", { name: "Apply" })).toBeNull();
    expect(r.queryByRole("button", { name: "Cancel" })).toBeNull();
    expect(r.queryByRole("button", { name: "View log" })).toBeNull();
    expect(levelSelect(r).disabled).toBe(true);
    fireEvent.click(button(r, "Close"));
    expect(r.onClose).toHaveBeenCalledOnce();
  });
});

describe("AutostartDialog: a request that is refused", () => {
  const refusing = (status: number, code: string, details: Record<string, unknown> = {}) =>
    stubApi({ "POST /jobs": () => apiError(status, code, `the message of ${code}`, details) });

  it("shows why in the text of the code", async () => {
    refusing(409, "busy");
    const r = renderAutostart("enable", samba());
    fireEvent.click(button(r, "Apply"));
    await settle();
    expect(r.getByRole("alert").textContent).toBe("Another change is running – try again in a moment.");
    expect(r.onReload).not.toHaveBeenCalled();
    expect(button(r, "Apply").disabled).toBe(false);
  });

  it.each([
    [
      400,
      "invalid_input",
      { field: "kind" },
      "The service has changed in the meantime. Reload the list and try again.",
    ],
    [404, "not_found", {}, "The service is gone – reload."],
  ])("reads the view again after %s %s, and leaves only Close", async (status, code, extra, text) => {
    const api = refusing(status, code, extra);
    const r = renderAutostart("enable", samba());
    fireEvent.click(button(r, "Apply"));
    await settle();
    expect(r.onReload).toHaveBeenCalledOnce();
    expect(r.getByRole("alert").textContent).toBe(text);
    // a second click would send a request that is refused again
    expect(r.queryByRole("button", { name: "Apply" })).toBeNull();
    expect(r.queryByRole("button", { name: "Cancel" })).toBeNull();
    expect(levelSelect(r).disabled).toBe(true);
    fireEvent.click(button(r, "Close"));
    expect(r.onClose).toHaveBeenCalledOnce();
    expect(api.sent("POST", "/jobs")).toHaveLength(1);
  });

  it("leaves only Close after a stale refusal of a warning dialog, and takes the callout away", async () => {
    refusing(404, "not_found");
    const r = renderAutostart("disable", sshd());
    fireEvent.click(r.getByRole("checkbox"));
    fireEvent.click(button(r, "Apply"));
    await settle();
    expect(r.getAllByRole("alert")).toHaveLength(1);
    expect(r.getByRole("alert").textContent).toBe("The service is gone – reload.");
    expect(r.queryByRole("checkbox")).toBeNull();
    expect(r.queryByRole("button", { name: "Apply" })).toBeNull();
  });
});

describe("AutostartDialog: a kind that no longer fits the service", () => {
  const text = "The service has changed in the meantime. Reload the list and try again.";
  const CASES: [string, AutostartKind, ServiceView][] = [
    ["switching on a service that is in default", "enable", docker()],
    ["switching on a service that is in boot", "enable", find(view(), "networking")],
    ["switching off a service that is in no runlevel", "disable", samba()],
  ];

  it.each(CASES)("says that the service changed, and sends nothing, for %s", (_what, kind, s) => {
    const api = routes(kind, s);
    const r = renderAutostart(kind, s);
    expect(r.getByRole("alert").textContent).toBe(text);
    expect(r.queryByRole("button", { name: "Apply" })).toBeNull();
    expect(r.queryByRole("combobox")).toBeNull();
    expect(r.queryByRole("checkbox")).toBeNull();
    fireEvent.click(r.getByRole("button", { name: "Close" }));
    expect(r.onClose).toHaveBeenCalledOnce();
    expect(api.calls).toEqual([]);
  });

  it("says it in German", () => {
    const r = renderAutostart("enable", docker(), "de");
    expect(r.getByRole("alert").textContent).toBe(
      "Der Dienst hat sich inzwischen geändert. Lade die Liste neu und versuche es noch einmal.",
    );
  });
});
