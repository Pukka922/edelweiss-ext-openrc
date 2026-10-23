import { cleanup, fireEvent } from "@solidjs/testing-library";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { button, DONE, renderAction, routes } from "../test/dialog";
import { docker, job, samba, sshd } from "../test/fixtures";
import { apiError, type Handler, jobRoutes, json, settle } from "../test/render";
import { POLL_MS } from "../useJob";

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("ActionDialog: a stop or restart", () => {
  describe("when the relations cannot be read", () => {
    // rc-service ineed, iuse, iafter or needsme failed: the whole answer of the server is an error
    const unreadable = (extra: Record<string, Handler> = {}) =>
      jobRoutes("stop", "docker", ["stop"], DONE, {
        "GET /services/docker": () => apiError(500, "failed", "reading the relations of docker: boom"),
        ...extra,
      });

    it("says that the services that stop along could not be read, and the action goes on without them", async () => {
      const api = unreadable();
      const r = renderAction("stop", docker());
      await settle();
      expect(r.getByRole("alert").textContent).toBe("Which services stop along with it could not be read.");
      expect(r.queryByText(/These stop along/)).toBeNull();
      expect(button(r, "Stop").disabled).toBe(false);
      fireEvent.click(button(r, "Stop"));
      await settle();
      expect(api.sent("POST", "/jobs").map((c) => c.body)).toEqual([{ kind: "stop", service: "docker" }]);
      await vi.advanceTimersByTimeAsync(POLL_MS);
      expect(r.onDone).toHaveBeenCalledExactlyOnceWith("docker was stopped.");
    });

    it("says it in German, for a restart as well", async () => {
      jobRoutes("restart", "docker", ["restart"], DONE, {
        "GET /services/docker": () => apiError(500, "failed", "boom"),
      });
      const r = renderAction("restart", docker(), { locale: "de" });
      await settle();
      expect(r.getByRole("alert").textContent).toBe(
        "Welche Dienste dabei mitgestoppt werden, ließ sich nicht lesen.",
      );
      expect(button(r, "Neu starten").disabled).toBe(false);
    });

    it("waits for the read all the same: the confirm is off until it has failed", async () => {
      let fail = () => {};
      const pending = new Promise<Response>((resolve) => {
        fail = () => resolve(apiError(500, "failed", "boom"));
      });
      unreadable({ "GET /services/docker": () => pending });
      const r = renderAction("stop", docker());
      expect(button(r, "Stop").disabled).toBe(true);
      expect(r.queryByRole("alert")).toBeNull();
      fail();
      await settle();
      expect(button(r, "Stop").disabled).toBe(false);
      expect(r.getByRole("alert")).toBeTruthy();
    });

    it("still asks for the check under a warning, and sends confirm: true", async () => {
      const api = jobRoutes("stop", "sshd", ["stop"], DONE, {
        "GET /services/sshd": () => apiError(500, "failed", "boom"),
      });
      const r = renderAction("stop", sshd());
      await settle();
      expect(r.getAllByRole("alert").map((a) => a.textContent)).toContain(
        "Which services stop along with it could not be read.",
      );
      expect(button(r, "Stop").disabled).toBe(true);
      fireEvent.click(r.getByRole("checkbox"));
      expect(button(r, "Stop").disabled).toBe(false);
      fireEvent.click(button(r, "Stop"));
      await settle();
      expect(api.sent("POST", "/jobs").map((c) => c.body)).toEqual([
        { kind: "stop", service: "sshd", confirm: true },
      ]);
    });

    it("leaves the decision to the server: the services it names replace the note", async () => {
      let posts = 0;
      const api = unreadable({
        "POST /jobs": () =>
          ++posts === 1
            ? apiError(409, "confirm_required", "needs a confirmation", {
                reason: "remote",
                services: ["sshd"],
              })
            : json(job({ id: "J9", kind: "stop", target: "docker", status: "done", steps: ["stop"] }), 202),
      });
      const r = renderAction("stop", docker());
      await settle();
      fireEvent.click(button(r, "Stop"));
      await settle();
      expect(r.getByText("These stop along with it: sshd")).toBeTruthy();
      expect(r.getAllByRole("alert")).toHaveLength(1); // the warning, not the note
      expect(r.queryByText("Which services stop along with it could not be read.")).toBeNull();
      fireEvent.click(r.getByRole("checkbox"));
      fireEvent.click(button(r, "Stop"));
      await settle();
      expect(api.sent("POST", "/jobs")).toHaveLength(2);
    });

    it("leaves the refusal of the server to its own text", async () => {
      unreadable({
        "POST /jobs": () => apiError(500, "failed", "the services that need docker cannot be read: boom"),
      });
      const r = renderAction("stop", docker());
      await settle();
      fireEvent.click(button(r, "Stop"));
      await settle();
      expect(r.getAllByRole("alert").map((a) => a.textContent)).toContain(
        "That failed: the services that need docker cannot be read: boom",
      );
    });

    it("says nothing when the read worked, and nothing for a start, which reads nothing", async () => {
      routes("stop", docker(), DONE, ["nginx"]);
      const r = renderAction("stop", docker());
      await settle();
      expect(r.queryByRole("alert")).toBeNull();
      cleanup();
      const samba_ = renderAction("start", samba());
      await settle();
      expect(samba_.queryByRole("alert")).toBeNull();
    });
  });
});
