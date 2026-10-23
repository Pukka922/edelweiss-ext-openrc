import { fakeSession } from "@edelweiss/sdk/testing";
import { cleanup, fireEvent, waitFor, within } from "@solidjs/testing-library";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Kind, ServiceView } from "./api";
import { Services } from "./Services";
import { details, find, view } from "./test/fixtures";
import { alertWith, names, renderPage, row, VISIBLE } from "./test/page";
import { adminOnRequest, apiError, json, renderExt, settle, stubApi } from "./test/render";

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

// What a click on the page does: the guard and the dialog hook, the details dialog, and the page for a user who has no
// admin mode or can never get it.
describe("Services", () => {
  describe("what a click asks for", () => {
    it("opens the dialog hook with the kind and the service", async () => {
      const r = renderPage();
      await r.findByRole("table");
      const cases: [string, Kind, string][] = [
        ["Logs docker", "logs", "docker"],
        ["Restart docker", "restart", "docker"],
        ["Stop docker", "stop", "docker"],
        ["Start samba", "start", "samba"],
      ];
      for (const [label, kind, name] of cases) {
        fireEvent.click(r.getByRole("button", { name: label }));
        await waitFor(() => expect(r.openDialog).toHaveBeenLastCalledWith(kind, find(view(), name)));
      }
      expect(r.openDialog).toHaveBeenCalledTimes(4);
    });

    it("turns the switch of the autostart into enable and disable", async () => {
      const r = renderPage();
      await r.findByRole("table");
      fireEvent.click(r.getByRole("switch", { name: "Autostart docker" }));
      await waitFor(() => expect(r.openDialog).toHaveBeenLastCalledWith("disable", find(view(), "docker")));
      fireEvent.click(r.getByRole("switch", { name: "Autostart samba" }));
      await waitFor(() => expect(r.openDialog).toHaveBeenLastCalledWith("enable", find(view(), "samba")));
    });

    it("asks for admin mode before a change, and opens nothing while it stays off", async () => {
      const session = fakeSession({ admin: false, canAdmin: true });
      const r = renderPage({ session });
      await r.findByRole("table");
      fireEvent.click(r.getByRole("button", { name: "Restart docker" }));
      await waitFor(() => expect(session.calls.requestAdmin).toBe(1));
      fireEvent.click(r.getByRole("switch", { name: "Autostart docker" }));
      await waitFor(() => expect(session.calls.requestAdmin).toBe(2));
      await new Promise((resolve) => setTimeout(resolve, 10));
      expect(r.openDialog).not.toHaveBeenCalled();
    });

    it("opens the dialog once admin mode is on", async () => {
      const r = renderPage({ session: adminOnRequest() });
      await r.findByRole("table");
      fireEvent.click(r.getByRole("button", { name: "Stop docker" }));
      expect(r.openDialog).not.toHaveBeenCalled();
      await waitFor(() =>
        expect(r.openDialog).toHaveBeenCalledExactlyOnceWith("stop", find(view(), "docker")),
      );
    });

    describe("the view that admin mode brings", () => {
      /** The page with a view per request: the first as it is, the next ones as next() makes them. */
      function renderChanging(next: (v: ReturnType<typeof view>) => Response | undefined) {
        let reads = 0;
        const api = stubApi({
          "GET /view": () => {
            const v = view();
            return (reads++ > 0 ? next(v) : undefined) ?? json(v);
          },
        });
        const openDialog = vi.fn<(kind: Kind, s: ServiceView) => void>();
        const r = renderExt(() => <Services openDialog={openDialog} />, adminOnRequest());
        return { ...r, api, openDialog };
      }

      it("is read again once admin mode is on, and the dialog opens with the service of that view", async () => {
        // in admin mode the process reads more: docker turns out not to run
        const r = renderChanging((v) => {
          find(v, "docker").state = "stopped";
          return undefined;
        });
        await r.findByRole("table");
        fireEvent.click(r.getByRole("button", { name: "Restart docker" }));
        expect(r.openDialog).not.toHaveBeenCalled();
        await waitFor(() => expect(r.openDialog).toHaveBeenCalledOnce());
        expect(r.api.sent("GET", "/view")).toHaveLength(2);
        const [kind, current] = r.openDialog.mock.calls[0]!;
        expect(kind).toBe("restart");
        expect(current.state).toBe("stopped");
        // and the row of the page has it too
        expect(within(row(r, "docker")).getByText("stopped")).toBeTruthy();
      });

      it("opens the dialog of the autostart with the service of that view as well", async () => {
        const r = renderChanging((v) => {
          find(v, "samba").runlevels = ["default"];
          return undefined;
        });
        await r.findByRole("table");
        fireEvent.click(r.getByRole("switch", { name: "Autostart samba" }));
        await waitFor(() => expect(r.openDialog).toHaveBeenCalledOnce());
        expect(r.openDialog.mock.calls[0]?.[0]).toBe("enable");
        expect(r.openDialog.mock.calls[0]?.[1].runlevels).toEqual(["default"]);
      });

      it("opens nothing when the service has left it", async () => {
        const r = renderChanging((v) => {
          v.services = v.services.filter((s) => s.name !== "docker");
          return undefined;
        });
        await r.findByRole("table");
        fireEvent.click(r.getByRole("button", { name: "Stop docker" }));
        await waitFor(() => expect(r.api.sent("GET", "/view")).toHaveLength(2));
        await waitFor(() => expect(names(r)).not.toContain("docker"));
        await new Promise((resolve) => setTimeout(resolve, 10));
        expect(r.openDialog).not.toHaveBeenCalled();
      });

      it("opens nothing when it cannot be read, and the page shows why", async () => {
        const r = renderChanging(() => apiError(500, "failed", "cannot read rc-status"));
        await r.findByRole("table");
        fireEvent.click(r.getByRole("button", { name: "Stop docker" }));
        expect(await r.findByText(/cannot read rc-status/)).toBeTruthy();
        await new Promise((resolve) => setTimeout(resolve, 10));
        expect(r.openDialog).not.toHaveBeenCalled();
        // the last view stays on the page below the error
        expect(names(r)).toEqual(VISIBLE);
      });

      it("is not read again in admin mode, and not for the logs and the details", async () => {
        const r = renderPage({ routes: { "GET /services/docker": () => json(details("docker")) } });
        await r.findByRole("table");
        fireEvent.click(r.getByRole("button", { name: "Restart docker" }));
        await waitFor(() => expect(r.openDialog).toHaveBeenCalledOnce());
        fireEvent.click(r.getByRole("button", { name: "Logs docker" }));
        expect(r.openDialog).toHaveBeenCalledTimes(2);
        expect(r.api.sent("GET", "/view")).toHaveLength(1);
      });
    });

    it("asks for no admin mode for the logs and the details, which anybody may read", async () => {
      const session = fakeSession({ admin: false, canAdmin: true });
      const r = renderPage({ session, routes: { "GET /services/docker": () => json(details("docker")) } });
      await r.findByRole("table");
      fireEvent.click(r.getByRole("button", { name: "Logs docker" }));
      expect(r.openDialog).toHaveBeenCalledExactlyOnceWith("logs", find(view(), "docker"));
      fireEvent.click(r.getByRole("button", { name: "Details docker" }));
      expect(await r.findByRole("dialog", { name: "Details: docker" })).toBeTruthy();
      expect(session.calls.requestAdmin).toBe(0);
    });

    it("opens its own dialogs where no hook takes their place", async () => {
      stubApi({
        "GET /view": () => json(view()),
        "GET /services/docker": () => json(details("docker")),
        "GET /services/docker/logs": () =>
          json({ sources: [], source: "", lines: [], offset: 0, capped: false }),
      });
      const r = renderExt(() => <Services />);
      await r.findByRole("table");
      fireEvent.click(r.getByRole("button", { name: "Logs docker" }));
      expect(await r.findByRole("dialog", { name: "Logs: docker" })).toBeTruthy();
      fireEvent.click(r.getByRole("button", { name: "Close" }));
      fireEvent.click(r.getByRole("button", { name: "Restart docker" }));
      expect(await r.findByRole("dialog", { name: "Restart docker?" })).toBeTruthy();
    });
  });

  describe("the details", () => {
    it("opens the dialog of the service, loaded from /services/<name>, and closes it again", async () => {
      const r = renderPage({
        routes: { "GET /services/docker": () => json(details("docker", { needs: ["net"] })) },
      });
      await r.findByRole("table");
      fireEvent.click(r.getByRole("button", { name: "Details docker" }));
      const dialog = await r.findByRole("dialog", { name: "Details: docker" });
      expect(await within(dialog).findByText("Docker Daemon")).toBeTruthy();
      expect(r.api.sent("GET", "/services/docker")).toHaveLength(1);
      expect(r.openDialog).not.toHaveBeenCalled();
      fireEvent.click(within(dialog).getByRole("button", { name: "Close" }));
      expect(r.queryByRole("dialog")).toBeNull();
      // the list is still there
      expect(names(r)).toEqual(VISIBLE);
    });

    it("shows one dialog at a time, and the next service after Escape", async () => {
      const r = renderPage({
        routes: {
          "GET /services/nginx": () => json(details("nginx")),
          "GET /services/sshd": () => json(details("sshd")),
        },
      });
      await r.findByRole("table");
      fireEvent.click(r.getByRole("button", { name: "Details nginx" }));
      await r.findByRole("dialog", { name: "Details: nginx" });
      fireEvent.keyDown(document, { key: "Escape" });
      expect(r.queryByRole("dialog")).toBeNull();
      fireEvent.click(r.getByRole("button", { name: "Details sshd" }));
      await r.findByRole("dialog", { name: "Details: sshd" });
      expect(r.getAllByRole("dialog")).toHaveLength(1);
    });

    it("shows the details to a user who cannot change anything as well", async () => {
      const r = renderPage({
        session: fakeSession({ admin: false, canAdmin: false }),
        routes: { "GET /services/docker": () => json(details("docker")) },
      });
      await r.findByRole("table");
      fireEvent.click(r.getByRole("button", { name: "Details docker" }));
      expect(await r.findByRole("dialog", { name: "Details: docker" })).toBeTruthy();
    });

    it("keeps its dialog when the view is read again", async () => {
      vi.useFakeTimers();
      const r = renderPage({ routes: { "GET /services/docker": () => json(details("docker")) } });
      await settle();
      fireEvent.click(r.getByRole("button", { name: "Details docker" }));
      await settle();
      const dialog = r.getByRole("dialog", { name: "Details: docker" });
      await vi.advanceTimersByTimeAsync(10000);
      expect(r.api.sent("GET", "/view").length).toBeGreaterThan(1);
      expect(r.getByRole("dialog", { name: "Details: docker" })).toBe(dialog);
      expect(r.api.sent("GET", "/services/docker")).toHaveLength(1);
    });
  });

  describe("without admin mode", () => {
    it("says that a change asks for admin mode first", async () => {
      const r = renderPage({ session: fakeSession({ admin: false, canAdmin: true }), locale: "de" });
      await r.findByRole("table");
      expect(
        alertWith(
          r,
          "Ohne Admin-Modus siehst du alles, aber Starten, Stoppen und Autostart fragen zuerst nach dem Admin-Passwort.",
        ),
      ).toBeTruthy();
    });

    it("has no hint in admin mode", async () => {
      const r = renderPage();
      await r.findByRole("table");
      expect(r.queryAllByRole("alert").filter((a) => !a.className.includes("alert-error"))).toEqual([]);
    });
  });

  describe("for a user who cannot get admin mode", () => {
    const readOnly = () => fakeSession({ admin: false, canAdmin: false });

    it("shows the list and the hint, but no button of a change and no working switch", async () => {
      const r = renderPage({ session: readOnly(), locale: "de" });
      await r.findByRole("table");
      expect(names(r)).toEqual(VISIBLE);
      expect(alertWith(r, "Ändern können nur Administratoren.")?.textContent).toBe(
        "Ändern können nur Administratoren.",
      );
      expect(alertWith(r, "Admin-Modus")).toBeUndefined();
      const all = r.getAllByRole("button").map((b) => b.getAttribute("aria-label") ?? "");
      expect(all.filter((l) => /^(Neu starten|Stoppen|Starten) /.test(l))).toEqual([]);
      // what only reads stays
      expect(all).toContain("Logs docker");
      expect(all).toContain("Details docker");
      const switches = r.getAllByRole("switch") as HTMLInputElement[];
      expect(switches.length).toBeGreaterThan(0);
      expect(switches.every((s) => s.disabled)).toBe(true);
      // the state of the autostart is shown all the same
      expect((r.getByRole("switch", { name: "Autostart docker" }) as HTMLInputElement).checked).toBe(true);
    });

    it("says it in English too", async () => {
      const r = renderPage({ session: readOnly() });
      await r.findByRole("table");
      expect(alertWith(r, "Only administrators can change anything here.")).toBeTruthy();
    });
  });
});
