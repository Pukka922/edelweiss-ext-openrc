import { fakeSession } from "@edelweiss/sdk/testing";
import { cleanup, fireEvent, waitFor, within } from "@solidjs/testing-library";
import { afterEach, describe, expect, it, vi } from "vitest";
import { find, job, service, view } from "./test/fixtures";
import { alertWith, labels, names, type R, renderPage, row, VISIBLE, viewOf } from "./test/page";
import { settle, type } from "./test/render";

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

// The view of the page: the head, the list, the filter and the search, the banner, the states and the buttons. What a
// click does is in ServicesActions.test.tsx, what happens over time and when there is nothing to show in
// ServicesLive.test.tsx.
describe("Services", () => {
  describe("the head", () => {
    it("shows the title, and the numbers of the host", async () => {
      const r = renderPage();
      expect(r.getByRole("heading", { name: "Services" })).toBeTruthy();
      expect(await r.findByText("15 services · 8 running · 2 problems")).toBeTruthy();
    });

    it("speaks German", async () => {
      const r = renderPage({ locale: "de" });
      expect(r.getByRole("heading", { name: "Dienste" })).toBeTruthy();
      expect(await r.findByText("15 Dienste · 8 laufen · 2 Probleme")).toBeTruthy();
      expect(r.getByRole("button", { name: "Neu laden" })).toBeTruthy();
      expect(r.getByRole("searchbox", { name: "Dienst suchen …" })).toBeTruthy();
      for (const name of ["Alle", "Laufend", "Gestoppt", "Im Autostart"])
        expect(r.getByRole("button", { name }), name).toBeTruthy();
      expect(r.getByLabelText("Systemdienste anzeigen")).toBeTruthy();
    });

    it("leaves out the problems when there are none, and counts one in the singular", async () => {
      const one = viewOf([service({ name: "a" })]);
      const r = renderPage({ v: one });
      expect(await r.findByText("1 service · 1 running")).toBeTruthy();
      cleanup();
      const bad = viewOf([
        service({ name: "a", state: "crashed" }),
        service({ name: "b", state: "stopped" }),
      ]);
      const p = renderPage({ v: bad, locale: "de" });
      expect(await p.findByText("2 Dienste · 0 laufen · 1 Problem")).toBeTruthy();
      cleanup();
      const q = renderPage({ v: one, locale: "de" });
      expect(await q.findByText("1 Dienst · 1 läuft")).toBeTruthy();
    });

    it("shows that it loads, until the view is there", async () => {
      vi.useFakeTimers();
      const r = renderPage();
      expect(r.getByRole("status").textContent).toContain("Loading …");
      expect(r.queryByRole("table")).toBeNull();
      await settle();
      expect(r.queryByText("Loading …")).toBeNull();
      expect(r.getByRole("table")).toBeTruthy();
    });
  });

  describe("the list", () => {
    it("shows the services of the view, by name, without the system services", async () => {
      const r = renderPage();
      await r.findByRole("table");
      expect(names(r)).toEqual(VISIBLE);
      expect(r.getByText("nginx web server")).toBeTruthy();
      expect(r.getAllByRole("columnheader").map((th) => th.textContent)).toEqual([
        "Service",
        "Status",
        "Autostart",
        "Actions",
      ]);
    });

    it("shows the system services of sysinit and shutdown when asked, and hides them again", async () => {
      const r = renderPage();
      await r.findByRole("table");
      const show = r.getByLabelText("Show system services") as HTMLInputElement;
      expect(show.checked).toBe(false);
      fireEvent.click(show);
      expect(names(r)).toEqual([...VISIBLE, "devfs", "killprocs"].sort());
      // they have no switch for the autostart, and only logs and details as buttons
      expect(within(row(r, "devfs")).queryByRole("switch")).toBeNull();
      expect(labels(row(r, "devfs"))).toEqual(["Logs devfs", "Details devfs"]);
      fireEvent.click(show);
      expect(names(r)).toEqual(VISIBLE);
    });

    it("says what the system services are", async () => {
      const r = renderPage({ locale: "de" });
      await r.findByRole("table");
      expect(r.getByText(/sysinit und shutdown/)).toBeTruthy();
    });

    it("says when no service is left, instead of an empty table", async () => {
      const r = renderPage({ v: viewOf([]) });
      expect(await r.findByText("No service matches.")).toBeTruthy();
      expect(r.queryByRole("table")).toBeNull();
    });
  });

  describe("the filter and the search", () => {
    it("shows the services that run", async () => {
      const r = renderPage();
      await r.findByRole("table");
      const all = r.getByRole("button", { name: "All" });
      expect(all.getAttribute("aria-pressed")).toBe("true");
      fireEvent.click(r.getByRole("button", { name: "Running" }));
      expect(names(r)).toEqual(["chronyd", "docker", "edelweiss", "mdadm-raid", "networking", "sshd"]);
      expect(r.getByRole("button", { name: "Running" }).getAttribute("aria-pressed")).toBe("true");
      expect(all.getAttribute("aria-pressed")).toBe("false");
    });

    it("shows the services that are down: stopped, crashed, or their start failed", async () => {
      const r = renderPage();
      await r.findByRole("table");
      fireEvent.click(r.getByRole("button", { name: "Stopped" }));
      expect(names(r)).toEqual(["cups", "nginx", "redis", "samba"]);
    });

    it("shows the services of the autostart", async () => {
      const r = renderPage();
      await r.findByRole("table");
      fireEvent.click(r.getByRole("button", { name: "In the autostart" }));
      expect(names(r)).toEqual(VISIBLE.filter((n) => n !== "samba"));
      fireEvent.click(r.getByRole("button", { name: "All" }));
      expect(names(r)).toEqual(VISIBLE);
    });

    it("searches the name and the description, whatever the case", async () => {
      const r = renderPage();
      await r.findByRole("table");
      const search = r.getByRole("searchbox", { name: "Search services …" });
      type(search, "NGI");
      expect(names(r)).toEqual(["nginx"]);
      type(search, "web");
      expect(names(r)).toEqual(["edelweiss", "nginx"]);
      // spaces around the text do not count
      type(search, "  ngi ");
      expect(names(r)).toEqual(["nginx"]);
      type(search, "");
      expect(names(r)).toEqual(VISIBLE);
    });

    it("searches inside the filter, and says when nothing is left", async () => {
      const r = renderPage();
      await r.findByRole("table");
      fireEvent.click(r.getByRole("button", { name: "Stopped" }));
      type(r.getByRole("searchbox"), "web");
      expect(names(r)).toEqual(["nginx"]);
      type(r.getByRole("searchbox"), "docker");
      expect(r.getByText("No service matches.")).toBeTruthy();
      expect(r.queryByRole("table")).toBeNull();
    });

    it("does not show a system service that matches the search while they are hidden", async () => {
      const r = renderPage();
      await r.findByRole("table");
      type(r.getByRole("searchbox"), "devfs");
      expect(names(r)).toEqual([]);
      fireEvent.click(r.getByLabelText("Show system services"));
      expect(names(r)).toEqual(["devfs"]);
    });
  });

  describe("the red banner", () => {
    it("names a service that crashed, with logs and restart", async () => {
      const r = renderPage({ locale: "de" });
      await r.findByRole("table");
      const banner = alertWith(r, "nginx ist abgestürzt")!;
      expect(banner.className).toContain("alert-error");
      expect(labels(banner)).toEqual(["Logs nginx", "Neu starten nginx"]);
    });

    it("names a service whose start failed, with logs and start", async () => {
      const r = renderPage({ locale: "de" });
      await r.findByRole("table");
      const banner = alertWith(r, "redis ließ sich nicht starten")!;
      expect(labels(banner)).toEqual(["Logs redis", "Starten redis"]);
    });

    it("says when the start failed, when the view knows it", async () => {
      vi.useFakeTimers();
      vi.setSystemTime(Date.parse("2026-10-20T08:00:00Z"));
      const failed = service({ name: "redis", state: "failed", failedAt: "2026-10-20T07:56:00Z" });
      const r = renderPage({ v: viewOf([failed, service({ name: "web", state: "failed" })]), locale: "de" });
      await settle();
      expect(alertWith(r, "redis")?.textContent).toBe("redis ließ sich nicht starten (vor 4 min)");
      expect(alertWith(r, "web")?.textContent).toBe("web ließ sich nicht starten");
      cleanup();
      const e = renderPage({ v: viewOf([failed]) });
      await settle();
      expect(alertWith(e, "redis")?.textContent).toBe("redis could not be started (4 min ago)");
    });

    it("has a banner for each of them and for no other service", async () => {
      const r = renderPage();
      await r.findByRole("table");
      const banners = r.getAllByRole("alert").filter((a) => a.className.includes("alert-error"));
      expect(banners.map((a) => a.textContent)).toEqual(["nginx has crashed", "redis could not be started"]);
    });

    it("stays when a filter or the search leaves the service out", async () => {
      const r = renderPage();
      await r.findByRole("table");
      fireEvent.click(r.getByRole("button", { name: "Running" }));
      type(r.getByRole("searchbox"), "docker");
      expect(alertWith(r, "nginx has crashed")).toBeTruthy();
      expect(alertWith(r, "redis could not be started")).toBeTruthy();
    });

    it("names a crashed system service as well, with the logs only: it cannot be restarted", async () => {
      const crashed = service({ name: "devfs", class: "base", state: "crashed", runlevels: ["sysinit"] });
      const r = renderPage({ v: viewOf([crashed]) });
      const banner = await r.findByRole("alert");
      expect(banner.textContent).toBe("devfs has crashed");
      expect(labels(banner)).toEqual(["Logs devfs"]);
      // the system service itself is hidden
      expect(names(r)).toEqual([]);
    });

    it("asks for the logs and for the restart or start, with the service", async () => {
      const r = renderPage();
      await r.findByRole("table");
      const nginx = alertWith(r, "nginx has crashed")!;
      fireEvent.click(within(nginx).getByRole("button", { name: "Logs nginx" }));
      expect(r.openDialog).toHaveBeenLastCalledWith("logs", find(view(), "nginx"));
      fireEvent.click(within(nginx).getByRole("button", { name: "Restart nginx" }));
      await waitFor(() => expect(r.openDialog).toHaveBeenLastCalledWith("restart", find(view(), "nginx")));
      const redis = alertWith(r, "redis could not be started")!;
      fireEvent.click(within(redis).getByRole("button", { name: "Start redis" }));
      await waitFor(() => expect(r.openDialog).toHaveBeenLastCalledWith("start", find(view(), "redis")));
    });

    it("has the logs only for a user who cannot change anything", async () => {
      const r = renderPage({ session: fakeSession({ admin: false, canAdmin: false }) });
      await r.findByRole("table");
      expect(labels(alertWith(r, "nginx has crashed")!)).toEqual(["Logs nginx"]);
      expect(labels(alertWith(r, "redis could not be started")!)).toEqual(["Logs redis"]);
    });

    it("has a spinner instead of the restart while a job for the service runs, next to the logs", async () => {
      const r = renderPage({ v: view({ job: job({ kind: "restart", target: "nginx" }) }) });
      await r.findByRole("table");
      const banner = alertWith(r, "nginx has crashed")!;
      expect(labels(banner)).toEqual(["Logs nginx"]);
      expect(within(banner).getByRole("status")).toBeTruthy();
      // redis has no job
      expect(labels(alertWith(r, "redis could not be started")!)).toEqual(["Logs redis", "Start redis"]);
    });
  });

  describe("the states", () => {
    it("shows a service of the autostart that does not run in yellow, without a red banner", async () => {
      const r = renderPage({ locale: "de" });
      await r.findByRole("table");
      const text = within(row(r, "cups")).getByText("im Autostart, läuft nicht");
      expect(text.className).toContain("text-warning");
      expect(alertWith(r, "cups")).toBeUndefined();
    });

    it("shows the other states as the preview does", async () => {
      const r = renderPage({ locale: "de" });
      await r.findByRole("table");
      const status = (name: string) => row(r, name).querySelectorAll("td")[1]?.textContent;
      expect(status("docker")).toBe("läuft");
      expect(status("networking")).toBe("gestartet (einmalig)");
      expect(status("samba")).toBe("gestoppt");
      expect(status("nginx")).toBe("abgestürzt");
      expect(status("redis")).toBe("Start fehlgeschlagen");
      expect(status("cronie")).toBe("startet …");
      expect(status("rsyslog")).toBe("stoppt …");
      expect(status("net.eth0")).toBe("inaktiv");
    });

    it("shows the badge of its class in each row", async () => {
      const r = renderPage({ locale: "de" });
      await r.findByRole("table");
      const badge = (name: string) => row(r, name).querySelector(".badge")?.textContent;
      expect(badge("edelweiss")).toBe("diese Oberfläche");
      expect(badge("sshd")).toBe("Fernzugang");
      expect(badge("networking")).toBe("Netzwerk");
      expect(badge("mdadm-raid")).toBe("Datenträger");
      expect(badge("docker")).toBeUndefined();
    });
  });

  describe("the buttons", () => {
    const buttonsOf = (r: R, name: string) => labels(row(r, name)).map((l) => l?.replace(` ${name}`, ""));

    it("have the actions of the state, as far as the verdict allows them", async () => {
      const r = renderPage();
      await r.findByRole("table");
      expect(buttonsOf(r, "docker")).toEqual(["Logs", "Restart", "Stop", "Details"]);
      expect(buttonsOf(r, "nginx")).toEqual(["Logs", "Restart", "Stop", "Details"]);
      expect(buttonsOf(r, "samba")).toEqual(["Logs", "Start", "Details"]);
      expect(buttonsOf(r, "redis")).toEqual(["Logs", "Start", "Details"]);
      expect(buttonsOf(r, "cups")).toEqual(["Logs", "Start", "Details"]);
      expect(buttonsOf(r, "net.eth0")).toEqual(["Logs", "Restart", "Stop", "Details"]);
      expect(buttonsOf(r, "networking")).toEqual(["Logs", "Restart", "Stop", "Details"]);
      // edelweiss is not stopped by its own page
      expect(buttonsOf(r, "edelweiss")).toEqual(["Logs", "Restart", "Details"]);
    });

    it("have a spinner instead of those that change it while a service starts or stops", async () => {
      const r = renderPage();
      await r.findByRole("table");
      for (const name of ["cronie", "rsyslog"]) {
        expect(buttonsOf(r, name), name).toEqual(["Logs", "Details"]);
        expect(within(row(r, name)).getByRole("status"), name).toBeTruthy();
      }
    });

    it("have a spinner instead of those that change it while a job for the service runs, and for no other", async () => {
      const r = renderPage({ v: view({ job: job({ kind: "stop", target: "docker", steps: ["stop"] }) }) });
      await r.findByRole("table");
      expect(buttonsOf(r, "docker")).toEqual(["Logs", "Details"]);
      expect(within(row(r, "docker")).getByRole("status")).toBeTruthy();
      expect(buttonsOf(r, "chronyd")).toEqual(["Logs", "Restart", "Stop", "Details"]);
      expect(r.getByText("Running: Stop docker")).toBeTruthy();
      expect(r.getByText("Step 1/1: stop the service")).toBeTruthy();
    });

    it("stay for an interrupted job, which only warns", async () => {
      const interrupted = job({
        status: "interrupted",
        code: "interrupted",
        target: "docker",
        kind: "stop",
        steps: ["stop"],
      });
      const r = renderPage({ v: view({ job: interrupted }) });
      await r.findByRole("table");
      expect(buttonsOf(r, "docker")).toEqual(["Logs", "Restart", "Stop", "Details"]);
      expect(alertWith(r, "Interrupted: Stop docker")).toBeTruthy();
    });
  });
});
