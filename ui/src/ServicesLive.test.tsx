import { fakeSession } from "@edelweiss/sdk/testing";
import { cleanup, fireEvent, waitFor, within } from "@solidjs/testing-library";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Services } from "./Services";
import { find, noOpenRC, service, view } from "./test/fixtures";
import { alertWith, names, renderPage, row, VISIBLE, viewOf } from "./test/page";
import { apiError, json, renderExt, settle, stubApi } from "./test/render";

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

// What happens to the page over time (the view is read again every few seconds), and what it shows when there is
// nothing to show: no OpenRC, or a view that cannot be read.
describe("Services", () => {
  describe("polling", () => {
    it("reads the view again every 5 seconds", async () => {
      vi.useFakeTimers();
      const r = renderPage();
      await vi.advanceTimersByTimeAsync(4990);
      expect(r.api.sent("GET", "/view")).toHaveLength(1);
      await vi.advanceTimersByTimeAsync(10);
      expect(r.api.sent("GET", "/view")).toHaveLength(2);
      await vi.advanceTimersByTimeAsync(5000);
      expect(r.api.sent("GET", "/view")).toHaveLength(3);
    });

    it("does not read it while the page is hidden", async () => {
      vi.useFakeTimers();
      const visibility = vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
      const r = renderPage();
      await vi.advanceTimersByTimeAsync(15000);
      expect(r.api.sent("GET", "/view")).toHaveLength(1);
      visibility.mockReturnValue("visible");
      await vi.advanceTimersByTimeAsync(5000);
      expect(r.api.sent("GET", "/view")).toHaveLength(2);
    });

    it("stops when the page is gone", async () => {
      vi.useFakeTimers();
      const r = renderPage();
      await settle();
      r.unmount();
      await vi.advanceTimersByTimeAsync(15000);
      expect(r.api.sent("GET", "/view")).toHaveLength(1);
    });

    it("reads it again on Reload", async () => {
      const r = renderPage();
      await r.findByRole("table");
      fireEvent.click(r.getByRole("button", { name: "Reload" }));
      await waitFor(() => expect(r.api.sent("GET", "/view")).toHaveLength(2));
    });

    it("keeps its rows, its buttons and the focus when the view is read again", async () => {
      vi.useFakeTimers();
      const r = renderPage();
      await settle();
      const buttons = r.getAllByRole("button");
      const rows = r.getAllByRole("row");
      const logs = r.getByRole("button", { name: "Logs docker" });
      logs.focus();
      await vi.advanceTimersByTimeAsync(5000);
      expect(r.api.sent("GET", "/view")).toHaveLength(2);
      expect(logs.isConnected).toBe(true);
      expect(document.activeElement).toBe(logs);
      const after = r.getAllByRole("button");
      expect(after).toHaveLength(buttons.length);
      expect(after.every((b, i) => b === buttons[i])).toBe(true);
      expect(r.getAllByRole("row").every((tr, i) => tr === rows[i])).toBe(true);
    });

    it("updates a row in place when its service changed", async () => {
      vi.useFakeTimers();
      let polls = 0;
      stubApi({
        "GET /view": () => {
          const changed = polls++ > 0;
          const v = view();
          if (changed) {
            // nginx came back, and docker stopped meanwhile
            find(v, "nginx").state = "started";
            find(v, "docker").state = "stopped";
            v.counts = { total: 15, running: 8, problems: 1 };
          }
          return json(v);
        },
      });
      const r = renderExt(() => <Services openDialog={() => {}} />);
      await settle();
      const nginx = row(r, "nginx");
      const logs = within(nginx).getByRole("button", { name: "Logs nginx" });
      expect(alertWith(r, "nginx has crashed")).toBeTruthy();
      expect(r.getByText("15 services · 8 running · 2 problems")).toBeTruthy();
      await vi.advanceTimersByTimeAsync(5000);
      expect(row(r, "nginx")).toBe(nginx);
      expect(within(nginx).getByRole("button", { name: "Logs nginx" })).toBe(logs);
      expect(within(nginx).getByText("running")).toBeTruthy();
      expect(alertWith(r, "nginx has crashed")).toBeUndefined();
      expect(r.getByText("15 services · 8 running · 1 problem")).toBeTruthy();
      expect(within(row(r, "docker")).getByText("stopped")).toBeTruthy();
      expect(within(row(r, "docker")).getByRole("button", { name: "Start docker" })).toBeTruthy();
      expect(within(row(r, "docker")).queryByRole("button", { name: "Stop docker" })).toBeNull();
    });

    it("says anew how long ago a start failed with every view", async () => {
      vi.useFakeTimers();
      vi.setSystemTime(Date.parse("2026-10-20T08:00:00Z"));
      const failed = service({ name: "redis", state: "failed", failedAt: "2026-10-20T07:56:00Z" });
      const r = renderPage({ v: viewOf([failed]) });
      await settle();
      const banner = alertWith(r, "redis could not be started")!;
      expect(banner.textContent).toBe("redis could not be started (4 min ago)");
      await vi.advanceTimersByTimeAsync(55_000);
      expect(r.api.sent("GET", "/view").length).toBeGreaterThan(10);
      expect(alertWith(r, "redis could not be started")).toBe(banner);
      expect(banner.textContent).toBe("redis could not be started (4 min ago)");
      await vi.advanceTimersByTimeAsync(5_000);
      expect(banner.textContent).toBe("redis could not be started (5 min ago)");
    });

    it("keeps the focus on the same service when a filter makes rows above it go", async () => {
      vi.useFakeTimers();
      let polls = 0;
      stubApi({
        "GET /view": () => {
          const v = view();
          // chronyd, above sshd in the list of running services, stops
          if (polls++ > 0) find(v, "chronyd").state = "stopped";
          return json(v);
        },
      });
      const r = renderExt(() => <Services openDialog={() => {}} />);
      await settle();
      fireEvent.click(r.getByRole("button", { name: "Running" }));
      expect(names(r)).toEqual(["chronyd", "docker", "edelweiss", "mdadm-raid", "networking", "sshd"]);
      const sshd = r.getByRole("button", { name: "Logs sshd" });
      sshd.focus();
      await vi.advanceTimersByTimeAsync(5000);
      expect(names(r)).toEqual(["docker", "edelweiss", "mdadm-raid", "networking", "sshd"]);
      expect(r.getByRole("button", { name: "Logs sshd" })).toBe(sshd);
      expect(document.activeElement).toBe(sshd);
      expect(sshd.closest("tr")).toBe(row(r, "sshd"));
    });

    it("shows a service that appears, and drops one that is gone", async () => {
      vi.useFakeTimers();
      let polls = 0;
      stubApi({
        "GET /view": () => {
          const v = view();
          if (polls++ > 0) {
            v.services = v.services.filter((s) => s.name !== "samba");
            v.services.push(service({ name: "zabbix", description: "monitoring" }));
          }
          return json(v);
        },
      });
      const r = renderExt(() => <Services openDialog={() => {}} />);
      await settle();
      expect(names(r)).toContain("samba");
      await vi.advanceTimersByTimeAsync(5000);
      expect(names(r)).not.toContain("samba");
      expect(names(r).at(-1)).toBe("zabbix");
      expect(within(row(r, "zabbix")).getByRole("button", { name: "Stop zabbix" })).toBeTruthy();
    });

    it("keeps the last view below the error when a reload fails, and shows the next one", async () => {
      vi.useFakeTimers();
      let polls = 0;
      stubApi({
        "GET /view": () => (polls++ === 1 ? apiError(500, "failed", "cannot read rc-status") : json(view())),
      });
      const r = renderExt(() => <Services openDialog={() => {}} />);
      await settle();
      await vi.advanceTimersByTimeAsync(5000);
      expect(alertWith(r, "That failed: cannot read rc-status")).toBeTruthy();
      expect(names(r)).toEqual(VISIBLE);
      await vi.advanceTimersByTimeAsync(5000);
      expect(alertWith(r, "cannot read rc-status")).toBeUndefined();
      expect(names(r)).toEqual(VISIBLE);
    });
  });

  describe("without OpenRC", () => {
    it("says so and shows nothing else", async () => {
      const r = renderPage({ v: noOpenRC(), locale: "de" });
      expect(await r.findByText("Auf diesem System läuft kein OpenRC.")).toBeTruthy();
      expect(r.getByRole("heading", { name: "Dienste" })).toBeTruthy();
      expect(r.queryByRole("table")).toBeNull();
      expect(r.queryByRole("searchbox")).toBeNull();
      expect(r.queryAllByRole("button")).toEqual([]);
      expect(r.queryAllByRole("switch")).toEqual([]);
      expect(r.queryByLabelText("Systemdienste anzeigen")).toBeNull();
      expect(r.queryAllByRole("alert")).toEqual([]);
    });

    it("says so in English, to a user who cannot change anything as well", async () => {
      const r = renderPage({ v: noOpenRC(), session: fakeSession({ admin: false, canAdmin: false }) });
      expect(await r.findByText("OpenRC is not running on this system.")).toBeTruthy();
      expect(r.queryAllByRole("alert")).toEqual([]);
    });

    it("shows the list when OpenRC runs at a later reload", async () => {
      vi.useFakeTimers();
      let polls = 0;
      stubApi({ "GET /view": () => json(polls++ === 0 ? noOpenRC() : view()) });
      const r = renderExt(() => <Services openDialog={() => {}} />);
      await settle();
      expect(r.queryByRole("table")).toBeNull();
      await vi.advanceTimersByTimeAsync(5000);
      expect(names(r)).toEqual(VISIBLE);
    });
  });

  describe("when the view cannot be read", () => {
    it("shows the error, and reads again on Retry", async () => {
      let polls = 0;
      const api = stubApi({
        "GET /view": () => (polls++ === 0 ? apiError(500, "failed", "cannot read rc-status") : json(view())),
      });
      const r = renderExt(() => <Services openDialog={() => {}} />);
      expect((await r.findByRole("alert")).textContent).toContain("That failed: cannot read rc-status");
      expect(r.queryByRole("table")).toBeNull();
      fireEvent.click(r.getByRole("button", { name: "Retry" }));
      await r.findByRole("table");
      expect(api.sent("GET", "/view")).toHaveLength(2);
      expect(names(r)).toEqual(VISIBLE);
    });
  });
});
