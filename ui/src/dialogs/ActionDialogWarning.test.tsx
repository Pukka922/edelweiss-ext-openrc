import { cleanup, fireEvent, within } from "@solidjs/testing-library";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { JobState, ServiceView } from "../api";
import { button, DONE, dialog, renderAction, routes } from "../test/dialog";
import { details, docker, find, nftables, service, sshd, view } from "../test/fixtures";
import { apiError, json, settle, stubApi } from "../test/render";
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

describe("ActionDialog: a change that needs the red warning", () => {
  it("shows the callout of sshd with the host of the page, and asks for the check before the red button works", async () => {
    const api = routes("stop", sshd(), DONE);
    const r = renderAction("stop", sshd());
    const d = dialog(r, "Stop sshd?");
    // the host is the one in the address bar of the browser, not the one the session knows
    expect(window.location.hostname).toBe("localhost");
    expect(within(d).getByRole("alert").textContent).toBe(
      "sshd is your remote access. Without sshd you can no longer reach localhost over SSH. The web interface stays available.",
    );
    const check = within(d).getByRole("checkbox", { name: "I know what I am doing" }) as HTMLInputElement;
    expect(check.checked).toBe(false);
    await settle();
    expect(button(r, "Stop").className).toContain("btn-error");
    expect(button(r, "Stop").disabled).toBe(true);
    fireEvent.click(button(r, "Stop"));
    await settle();
    expect(api.sent("POST", "/jobs")).toHaveLength(0);
    fireEvent.click(check);
    expect(button(r, "Stop").disabled).toBe(false);
    fireEvent.click(button(r, "Stop"));
    await settle();
    expect(api.sent("POST", "/jobs").map((c) => c.body)).toEqual([
      { kind: "stop", service: "sshd", confirm: true },
    ]);
    await vi.advanceTimersByTimeAsync(POLL_MS);
    expect(r.onDone).toHaveBeenCalledExactlyOnceWith("sshd was stopped.");
  });

  it("takes the check away again, and the red button with it", async () => {
    routes("stop", sshd(), DONE);
    const r = renderAction("stop", sshd());
    await settle();
    const check = r.getByRole("checkbox") as HTMLInputElement;
    fireEvent.click(check);
    expect(button(r, "Stop").disabled).toBe(false);
    fireEvent.click(check);
    expect(button(r, "Stop").disabled).toBe(true);
  });

  it("speaks German, in the words of the preview", async () => {
    routes("stop", sshd(), DONE);
    const r = renderAction("stop", sshd(), { locale: "de" });
    const d = dialog(r, "sshd stoppen?");
    expect(within(d).getByRole("alert").textContent).toBe(
      "sshd ist dein Fernzugang. Ohne sshd kommst du nicht mehr per SSH auf localhost. Die Web-Oberfläche bleibt erreichbar.",
    );
    expect(within(d).getByRole("checkbox", { name: "Ich weiß, was ich tue" })).toBeTruthy();
    expect(button(r, "Abbrechen")).toBeTruthy();
    expect(button(r, "Stoppen").className).toContain("btn-error");
  });

  it("names the service of the way in that it is about: dropbear as well as sshd", async () => {
    const dropbear = service({ name: "dropbear", class: "remote" });
    routes("stop", dropbear, DONE);
    const r = renderAction("stop", dropbear, { locale: "de" });
    expect(dialog(r, "dropbear stoppen?")).toBeTruthy();
    expect(r.getByRole("alert").textContent).toBe(
      "dropbear ist dein Fernzugang. Ohne dropbear kommst du nicht mehr per SSH auf localhost. Die Web-Oberfläche bleibt erreichbar.",
    );
  });

  it("names the services that stop along in the warning of a restart, too", async () => {
    routes("restart", sshd(), DONE, ["nginx", "samba"]);
    const r = renderAction("restart", sshd());
    await settle();
    expect(r.getByText("These stop along with it: nginx")).toBeTruthy();
    fireEvent.click(r.getByRole("checkbox"));
    fireEvent.click(button(r, "Restart"));
    await settle();
    expect(r.getByText("Step 1/1: restart the service")).toBeTruthy();
  });

  // each class has its own warning; the text of the class decides, whichever service it is
  const CLASSES: [string, ServiceView, ActionKind, string][] = [
    ["network", find(view(), "net.eth0"), "stop", "This affects the network – and with it this interface."],
    ["firewall", nftables(), "start", "New firewall rules can lock you out."],
    [
      "storage",
      find(view(), "mdadm-raid"),
      "restart",
      "File systems are unmounted, RAID arrays are stopped.",
    ],
  ];
  it.each(CLASSES)(
    "warns about %s in its own words and sends confirm: true",
    async (_class, s, kind, text) => {
      const api = routes(kind, s, DONE);
      const r = renderAction(kind, s);
      await settle();
      expect(r.getByRole("alert").textContent).toBe(text);
      fireEvent.click(r.getByRole("checkbox"));
      fireEvent.click(button(r, { start: "Start", stop: "Stop", restart: "Restart" }[kind]));
      await settle();
      expect(api.sent("POST", "/jobs").map((c) => c.body)).toEqual([
        { kind, service: s.name, confirm: true },
      ]);
    },
  );

  it("reads no relations for the start of a service that needs the warning", async () => {
    const nft = nftables();
    const api = routes("start", nft, DONE);
    renderAction("start", nft);
    await settle();
    expect(api.calls).toEqual([]);
  });

  describe("that the server asks for after the plain confirm", () => {
    // the confirm of docker was plain, but a service that needs it is of a class with a warning
    function renderAsked(services: string[] = ["sshd"]) {
      let posts = 0;
      const api = stubApi({
        "GET /services/docker": () => json(details("docker", { neededBy: ["nginx", "cronie"] })),
        "POST /jobs": () =>
          ++posts === 1
            ? apiError(409, "confirm_required", "stop of docker needs a confirmation", {
                reason: "remote",
                services,
              })
            : json(
                {
                  id: "J9",
                  kind: "stop",
                  target: "docker",
                  status: "done",
                  steps: ["stop"],
                  step: 0,
                  started: "2026-10-20T12:00:00Z",
                },
                202,
              ),
      });
      const r = renderAction("stop", docker());
      return { ...r, api };
    }

    it("shows the warning of its reason and the services it names instead of those of the relations", async () => {
      const r = renderAsked(["sshd", "dropbear"]);
      await settle();
      expect(r.getByText("These stop along with it: nginx, cronie")).toBeTruthy();
      expect(r.queryByRole("checkbox")).toBeNull();
      fireEvent.click(button(r, "Stop"));
      await settle();
      expect(r.api.sent("POST", "/jobs").map((c) => c.body)).toEqual([{ kind: "stop", service: "docker" }]);
      expect(r.getByRole("alert").textContent).toContain("sshd is your remote access");
      expect(r.getByText("These stop along with it: sshd, dropbear")).toBeTruthy();
      expect(r.queryByText(/cronie/)).toBeNull();
      // it is the warning, not an error of the request
      expect(r.queryByText("This needs your confirmation.")).toBeNull();
      expect(r.getAllByRole("alert")).toHaveLength(1);
      expect(r.onDone).not.toHaveBeenCalled();
    });

    it("sends confirm: true once the check is set, and not before", async () => {
      const r = renderAsked();
      await settle();
      fireEvent.click(button(r, "Stop"));
      await settle();
      expect(button(r, "Stop").disabled).toBe(true);
      expect(button(r, "Stop").className).toContain("btn-error");
      fireEvent.click(r.getByRole("checkbox", { name: "I know what I am doing" }));
      fireEvent.click(button(r, "Stop"));
      await settle();
      expect(r.api.sent("POST", "/jobs").map((c) => c.body)).toEqual([
        { kind: "stop", service: "docker" },
        { kind: "stop", service: "docker", confirm: true },
      ]);
      expect(r.onDone).toHaveBeenCalledExactlyOnceWith("docker was stopped.");
    });

    it("names the service that stops along whose class asks for the warning, not the one stopped", async () => {
      const r = renderAsked(["nginx", "sshd"]);
      await settle();
      fireEvent.click(button(r, "Stop"));
      await settle();
      expect(r.getByRole("alert").textContent).toBe(
        "sshd is your remote access. Without sshd you can no longer reach localhost over SSH. The web interface stays available.",
      );
    });

    it("names none when the server names none", async () => {
      const r = renderAsked([]);
      await settle();
      fireEvent.click(button(r, "Stop"));
      await settle();
      expect(r.queryByText(/stop along/)).toBeNull();
      expect(r.getByRole("alert")).toBeTruthy();
    });

    it("asks for the check again when the server asks again", async () => {
      let posts = 0;
      stubApi({
        "GET /services/docker": () => json(details("docker")),
        "POST /jobs": () =>
          ++posts <= 2
            ? apiError(409, "confirm_required", "needs a confirmation", {
                reason: posts === 1 ? "network" : "firewall",
                services: [],
              })
            : json({}, 202),
      });
      const r = renderAction("stop", docker());
      await settle();
      fireEvent.click(button(r, "Stop"));
      await settle();
      expect(r.getByRole("alert").textContent).toContain("the network");
      fireEvent.click(r.getByRole("checkbox"));
      fireEvent.click(button(r, "Stop"));
      await settle();
      expect(r.getByRole("alert").textContent).toBe("New firewall rules can lock you out.");
      expect((r.getByRole("checkbox") as HTMLInputElement).checked).toBe(false);
      expect(button(r, "Stop").disabled).toBe(true);
    });
  });
});

describe("ActionDialog: a job that fails", () => {
  const FAILED: Partial<JobState> = {
    status: "failed",
    code: "failed",
    error: "rc-service nginx start: exit status 1",
    output:
      " * Starting nginx ...\nnginx: [emerg] bind() to 0.0.0.0:80 failed (98: Address in use)\n * Failed to start nginx  [ !! ]",
  };

  it("shows the output of OpenRC in a pre, and View log and Close instead of the buttons of the change", async () => {
    const redis = find(view(), "redis");
    routes("start", redis, [FAILED]);
    const r = renderAction("start", redis);
    fireEvent.click(button(r, "Start"));
    await settle();
    await vi.advanceTimersByTimeAsync(POLL_MS);
    const d = dialog(r, "redis could not be started");
    expect(within(d).queryByText("redis will be started.")).toBeNull();
    expect(d.querySelector("pre")?.textContent).toBe(FAILED.output);
    expect(within(d).getByRole("alert").textContent).toContain(
      "Step 1/1 (start the service) failed: rc-service nginx start: exit status 1",
    );
    expect(r.queryByRole("button", { name: "Start" })).toBeNull();
    expect(r.queryByRole("button", { name: "Cancel" })).toBeNull();
    fireEvent.click(button(r, "View log"));
    expect(r.onLogs).toHaveBeenCalledOnce();
    expect(r.onClose).not.toHaveBeenCalled();
    fireEvent.click(button(r, "Close"));
    expect(r.onClose).toHaveBeenCalledOnce();
    expect(r.onDone).not.toHaveBeenCalled();
  });

  it("is titled in German by what failed", async () => {
    const nginx = find(view(), "nginx");
    for (const [kind, title] of [
      ["start", "nginx ließ sich nicht starten"],
      ["stop", "nginx ließ sich nicht stoppen"],
      ["restart", "nginx ließ sich nicht neu starten"],
    ] as const) {
      routes(kind, nginx, [FAILED]);
      const s = kind === "start" ? service({ name: "nginx", state: "stopped" }) : nginx;
      const r = renderAction(kind, s, { locale: "de" });
      await settle();
      fireEvent.click(button(r, { start: "Starten", stop: "Stoppen", restart: "Neu starten" }[kind]));
      await settle();
      await vi.advanceTimersByTimeAsync(POLL_MS);
      expect(dialog(r, title), kind).toBeTruthy();
      expect(r.queryByText(/wird (neu )?gestartet\.|wird gestoppt\./), kind).toBeNull();
      expect(button(r, "Log ansehen"), kind).toBeTruthy();
      expect(button(r, "Schließen"), kind).toBeTruthy();
      cleanup();
    }
  });

  it.each([
    ["interrupted", { status: "interrupted", code: "interrupted" }, "The job was interrupted at step 1/1"],
    ["failed", { status: "failed", code: "failed", error: "exit status 1" }, "failed: exit status 1"],
  ] as const)(
    "shows a job that %s without output as the alert, and leaves only Close",
    async (_what, state, text) => {
      const redis = find(view(), "redis");
      routes("start", redis, [state]);
      const r = renderAction("start", redis);
      fireEvent.click(button(r, "Start"));
      await settle();
      await vi.advanceTimersByTimeAsync(POLL_MS);
      expect(r.getByRole("alert").textContent).toContain(text);
      // no output, so no log to look at, and no title of a failed start; the job is over, so no second try of it
      expect(dialog(r, "Start redis?")).toBeTruthy();
      expect(r.getByText("redis will be started.")).toBeTruthy();
      expect(r.queryByRole("button", { name: "View log" })).toBeNull();
      expect(r.queryByRole("button", { name: "Start" })).toBeNull();
      expect(r.queryByRole("button", { name: "Cancel" })).toBeNull();
      fireEvent.click(button(r, "Close"));
      expect(r.onClose).toHaveBeenCalledOnce();
      expect(r.onDone).not.toHaveBeenCalled();
    },
  );

  it("keeps a failure of the warning dialog with the output too", async () => {
    routes("stop", sshd(), [FAILED]);
    const r = renderAction("stop", sshd());
    await settle();
    fireEvent.click(r.getByRole("checkbox"));
    fireEvent.click(button(r, "Stop"));
    await settle();
    await vi.advanceTimersByTimeAsync(POLL_MS);
    expect(dialog(r, "sshd could not be stopped").querySelector("pre")?.textContent).toBe(FAILED.output);
  });
});
