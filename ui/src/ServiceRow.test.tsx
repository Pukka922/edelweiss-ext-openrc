import { cleanup, fireEvent, within } from "@solidjs/testing-library";
import { createSignal } from "solid-js";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ServiceRow } from "./ServiceRow";
import { find, service, view } from "./test/fixtures";
import { renderExt } from "./test/render";
import { type Over, renderRow } from "./test/row";

afterEach(cleanup);

/** The names of the buttons of the last cell, in order, without the name of the service. */
const buttons = (r: ReturnType<typeof renderRow>, name: string) =>
  within(r.container.querySelector("td:last-child") as HTMLElement)
    .queryAllByRole("button")
    .map((b) => b.getAttribute("aria-label")?.replace(` ${name}`, ""));

describe("ServiceRow", () => {
  describe("the name and the description", () => {
    it("shows the name and the description as they are", () => {
      const r = renderRow({ name: "nginx", description: "nginx web <server> $&" });
      expect(r.getByText("nginx")).toBeTruthy();
      expect(r.getByText("nginx web <server> $&")).toBeTruthy();
    });

    it("says after the description that a supervised service restarts after a crash", () => {
      const r = renderRow(find(view(), "docker"), { locale: "de" });
      expect(r.getByText("Docker Daemon · überwacht (startet nach Absturz neu)")).toBeTruthy();
      cleanup();
      const en = renderRow(find(view(), "docker"));
      expect(en.getByText("Docker Daemon · supervised (restarts after a crash)")).toBeTruthy();
    });

    it("has only the supervision for a supervised service without a description", () => {
      const r = renderRow({ name: "x", supervised: true });
      expect(r.getByText("supervised (restarts after a crash)")).toBeTruthy();
    });
  });

  describe("the badge of the class", () => {
    const badge = (r: ReturnType<typeof renderRow>) =>
      [...r.container.querySelectorAll(".badge")].map((b) => b.textContent);

    it("names edelweiss, the way in from outside, the network, the firewall and the disks", () => {
      const cases: [Over["class"], string, string][] = [
        ["edelweiss", "this interface", "diese Oberfläche"],
        ["remote", "remote access", "Fernzugang"],
        ["network", "network", "Netzwerk"],
        ["firewall", "firewall", "Firewall"],
        ["storage", "storage", "Datenträger"],
      ];
      for (const [cls, english, german] of cases) {
        expect(badge(renderRow({ name: "x", class: cls })), cls).toEqual([english]);
        cleanup();
        expect(badge(renderRow({ name: "x", class: cls }, { locale: "de" })), cls).toEqual([german]);
        cleanup();
      }
    });

    it("has none for the base of the system and for the rest", () => {
      expect(badge(renderRow({ name: "devfs", class: "base", runlevels: ["sysinit"] }))).toEqual([]);
      cleanup();
      expect(badge(renderRow({ name: "nginx", class: "free" }))).toEqual([]);
    });
  });

  describe("the status", () => {
    const status = (r: ReturnType<typeof renderRow>) => r.container.querySelectorAll("td")[1] as HTMLElement;
    const dot = (r: ReturnType<typeof renderRow>) => status(r).querySelector("span[aria-hidden]")?.className;

    it("shows a started service as running, with a green dot", () => {
      const r = renderRow({ name: "chronyd" });
      expect(status(r).textContent).toBe("running");
      expect(dot(r)).toContain("bg-success");
    });

    it("shows a started one-shot service as started once, with a green dot", () => {
      const r = renderRow({ name: "networking", oneShot: true, class: "network" }, { locale: "de" });
      expect(status(r).textContent).toBe("gestartet (einmalig)");
      expect(dot(r)).toContain("bg-success");
    });

    it("shows a stopped service as stopped, with a grey dot", () => {
      const r = renderRow({ name: "samba", state: "stopped", runlevels: [] }, { locale: "de" });
      expect(status(r).textContent).toBe("gestoppt");
      expect(dot(r)).toContain("bg-base-content");
      expect(dot(r)).not.toContain("bg-warning");
    });

    it("shows a stopped service of the autostart in yellow: it is not running", () => {
      const r = renderRow({ name: "cups", state: "stopped", autostartIdle: true }, { locale: "de" });
      const text = within(status(r)).getByText("im Autostart, läuft nicht");
      expect(text.className).toContain("text-warning");
      expect(dot(r)).toContain("bg-warning");
    });

    it("shows a crashed service and one whose start failed in red", () => {
      for (const [state, text] of [
        ["crashed", "crashed"],
        ["failed", "start failed"],
      ] as const) {
        const r = renderRow({ name: "nginx", state });
        expect(within(status(r)).getByText(text).className, state).toContain("text-error");
        expect(dot(r), state).toContain("bg-error");
        cleanup();
      }
    });

    it("shows a service that starts, stops or is inactive with a yellow dot", () => {
      for (const [state, text] of [
        ["starting", "starting …"],
        ["stopping", "stopping …"],
        ["inactive", "inactive"],
      ] as const) {
        const r = renderRow({ name: "x", state });
        expect(status(r).textContent, state).toBe(text);
        expect(dot(r), state).toContain("bg-warning");
        cleanup();
      }
    });
  });

  describe("the buttons", () => {
    it("has logs, restart, stop and details for a service that runs, in this order", () => {
      expect(buttons(renderRow({ name: "chronyd" }), "chronyd")).toEqual([
        "Logs",
        "Restart",
        "Stop",
        "Details",
      ]);
    });

    it("has the same for an inactive service and for a one-shot one", () => {
      const all = ["Logs", "Restart", "Stop", "Details"];
      expect(buttons(renderRow({ name: "net.eth0", state: "inactive" }), "net.eth0")).toEqual(all);
      cleanup();
      expect(buttons(renderRow({ name: "mdadm-raid", oneShot: true }), "mdadm-raid")).toEqual(all);
    });

    it("has the same for a crashed service", () => {
      expect(buttons(renderRow({ name: "nginx", state: "crashed" }), "nginx")).toEqual([
        "Logs",
        "Restart",
        "Stop",
        "Details",
      ]);
    });

    it("has logs, start and details for a stopped service and for one whose start failed", () => {
      expect(buttons(renderRow({ name: "samba", state: "stopped" }), "samba")).toEqual([
        "Logs",
        "Start",
        "Details",
      ]);
      cleanup();
      expect(buttons(renderRow({ name: "redis", state: "failed" }), "redis")).toEqual([
        "Logs",
        "Start",
        "Details",
      ]);
    });

    it("words them in German", () => {
      const r = renderRow({ name: "chronyd" }, { locale: "de" });
      expect(buttons(r, "chronyd")).toEqual(["Logs", "Neu starten", "Stoppen", "Details"]);
      cleanup();
      expect(buttons(renderRow({ name: "samba", state: "stopped" }, { locale: "de" }), "samba")).toEqual([
        "Logs",
        "Starten",
        "Details",
      ]);
    });

    it("shows the text of a button as its tooltip, and the stop button in red", () => {
      const r = renderRow({ name: "chronyd" }, { locale: "de" });
      const stop = r.getByRole("button", { name: "Stoppen chronyd" });
      expect(stop.closest("[data-tip]")?.getAttribute("data-tip")).toBe("Stoppen");
      expect(stop.className).toContain("text-error");
      expect(r.getByRole("button", { name: "Neu starten chronyd" }).className).not.toContain("text-error");
    });

    it("leaves out a button whose verdict is deny: edelweiss cannot be stopped, only restarted", () => {
      const r = renderRow(find(view(), "edelweiss"));
      expect(buttons(r, "edelweiss")).toEqual(["Logs", "Restart", "Details"]);
    });

    it("has only logs and details for a service of the base", () => {
      expect(buttons(renderRow(find(view(), "devfs")), "devfs")).toEqual(["Logs", "Details"]);
      cleanup();
      const stopped = service({ name: "root", class: "base", state: "stopped", runlevels: ["boot"] });
      expect(buttons(renderRow(stopped), "root")).toEqual(["Logs", "Details"]);
    });

    it("shows a button whose verdict is warn: the dialog asks for the confirmation", () => {
      expect(buttons(renderRow(find(view(), "sshd")), "sshd")).toEqual([
        "Logs",
        "Restart",
        "Stop",
        "Details",
      ]);
      cleanup();
      const firewall = service({ name: "nftables", class: "firewall", state: "stopped" });
      expect(buttons(renderRow(firewall), "nftables")).toEqual(["Logs", "Start", "Details"]);
    });

    it("follows the verdict of each button on its own", () => {
      const s = service({
        name: "x",
        allowed: { start: "allow", restart: "deny", stop: "allow", enable: "allow", disable: "allow" },
      });
      expect(buttons(renderRow(s), "x")).toEqual(["Logs", "Stop", "Details"]);
    });

    it("has only logs and details for a user who cannot change anything", () => {
      expect(buttons(renderRow({ name: "chronyd" }, { readOnly: true }), "chronyd")).toEqual([
        "Logs",
        "Details",
      ]);
      cleanup();
      expect(buttons(renderRow({ name: "samba", state: "stopped" }, { readOnly: true }), "samba")).toEqual([
        "Logs",
        "Details",
      ]);
    });

    it("asks onAction for the dialog of a button, with the service", () => {
      const r = renderRow({ name: "chronyd", description: "NTP" });
      for (const [label, kind] of [
        ["Logs chronyd", "logs"],
        ["Restart chronyd", "restart"],
        ["Stop chronyd", "stop"],
        ["Details chronyd", "details"],
      ] as const) {
        fireEvent.click(r.getByRole("button", { name: label }));
        expect(r.onAction).toHaveBeenLastCalledWith(kind, r.service);
      }
      expect(r.onAction).toHaveBeenCalledTimes(4);
      cleanup();
      const stopped = renderRow({ name: "samba", state: "stopped" });
      fireEvent.click(stopped.getByRole("button", { name: "Start samba" }));
      expect(stopped.onAction).toHaveBeenCalledExactlyOnceWith("start", stopped.service);
    });
  });

  describe("while the service changes", () => {
    /** What the last cell holds, in order: the buttons by their text, and the spinner as "status". */
    const cell = (r: ReturnType<typeof renderRow>, name: string) =>
      [...(r.container.querySelector("td:last-child")?.querySelectorAll("button, [role=status]") ?? [])].map(
        (el) => (el.tagName === "BUTTON" ? el.getAttribute("aria-label")?.replace(` ${name}`, "") : "status"),
      );

    it("shows a spinner instead of the buttons that change it while it starts or stops, between Logs and Details", () => {
      for (const state of ["starting", "stopping"] as const) {
        const r = renderRow({ name: "cronie", state }, { locale: "de" });
        expect(cell(r, "cronie"), state).toEqual(["Logs", "status", "Details"]);
        expect(r.getByRole("status", { name: "Änderung läuft …" }).className, state).toContain("loading");
        cleanup();
      }
    });

    it("shows a spinner while a job for the service runs, with Logs and Details", () => {
      const r = renderRow({ name: "nginx" }, { busy: true });
      expect(cell(r, "nginx")).toEqual(["Logs", "status", "Details"]);
      expect(r.getByRole("status", { name: "Change in progress …" })).toBeTruthy();
    });

    it("keeps Logs and Details working, for a user who cannot change anything too", () => {
      const r = renderRow({ name: "cronie", state: "starting" }, { readOnly: true });
      expect(cell(r, "cronie")).toEqual(["Logs", "status", "Details"]);
      fireEvent.click(r.getByRole("button", { name: "Logs cronie" }));
      expect(r.onAction).toHaveBeenLastCalledWith("logs", r.service);
      fireEvent.click(r.getByRole("button", { name: "Details cronie" }));
      expect(r.onAction).toHaveBeenLastCalledWith("details", r.service);
    });

    it("keeps the switch of the autostart and the status", () => {
      const r = renderRow({ name: "cronie", state: "starting" });
      expect(r.getByRole("switch", { name: "Autostart cronie" })).toBeTruthy();
      expect(r.getByText("starting …")).toBeTruthy();
    });
  });

  describe("when the view is read again", () => {
    it("keeps its buttons and their focus, and changes only what changed", () => {
      const [svc, setSvc] = createSignal(service({ name: "nginx", state: "started", description: "web" }));
      const onAction = vi.fn();
      const r = renderExt(() => (
        <table>
          <tbody>
            <ServiceRow service={svc()} readOnly={false} onAction={onAction} />
          </tbody>
        </table>
      ));
      const logs = r.getByRole("button", { name: "Logs nginx" });
      const details = r.getByRole("button", { name: "Details nginx" });
      const sw = r.getByRole("switch", { name: "Autostart nginx" });
      logs.focus();
      expect(r.getByRole("button", { name: "Stop nginx" })).toBeTruthy();
      // the next view: nginx stopped, taken out of the autostart, with a new description
      setSvc(service({ name: "nginx", state: "stopped", runlevels: [], description: "web server" }));
      expect(r.queryByRole("button", { name: "Stop nginx" })).toBeNull();
      expect(r.getByRole("button", { name: "Start nginx" })).toBeTruthy();
      expect(r.getByRole("button", { name: "Logs nginx" })).toBe(logs);
      expect(r.getByRole("button", { name: "Details nginx" })).toBe(details);
      expect(r.getByRole("switch", { name: "Autostart nginx" })).toBe(sw);
      expect((sw as HTMLInputElement).checked).toBe(false);
      expect(document.activeElement).toBe(logs);
      expect(r.getByText("web server")).toBeTruthy();
      expect(r.getByText("stopped")).toBeTruthy();
      // a click asks with the service of the new view
      fireEvent.click(logs);
      expect(onAction).toHaveBeenLastCalledWith("logs", svc());
    });
  });
});
