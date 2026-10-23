import { cleanup, fireEvent, within } from "@solidjs/testing-library";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { find, job, view } from "../test/fixtures";
import { apiError, json, renderExt, settle, stubApi } from "../test/render";
import { RESTART_GIVE_UP_MS, RESTART_POLL_MS, RestartEdelweissDialog } from "./RestartEdelweissDialog";

let reload: ReturnType<typeof vi.fn>;

beforeEach(() => {
  vi.useFakeTimers();
  reload = vi.fn();
  vi.stubGlobal("location", { ...window.location, reload });
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

const edelweiss = () => find(view(), "edelweiss");

/** The routes of the restart of edelweiss; session answers GET /api/session, one answer per request (the last one stays). */
function routes(
  session: (() => Response | Promise<Response>)[] = [() => json({}, 200)],
  post?: () => Response,
) {
  let n = 0;
  return stubApi({
    "POST /jobs": post ?? (() => json(job({ id: "J9", kind: "restart", target: "edelweiss" }), 202)),
    "GET /api/session": () => session[Math.min(n++, session.length - 1)]!(),
  });
}

function renderDialog(locale?: "en" | "de") {
  const hooks = { onClose: vi.fn(), onReload: vi.fn() };
  const r = renderExt(() => <RestartEdelweissDialog service={edelweiss()} {...hooks} />, undefined, locale);
  return { ...r, ...hooks };
}

const restart = (r: ReturnType<typeof renderDialog>) =>
  fireEvent.click(r.getByRole("button", { name: "Restart" }));

const noAnswer = (): Response => {
  throw new TypeError("Failed to fetch");
};
const status = (code: number) => () => new Response(null, { status: code });

describe("RestartEdelweissDialog", () => {
  it("tells that the page is gone for a few seconds and that the sessions end, and asks first", () => {
    const api = routes();
    const r = renderDialog();
    const d = r.getByRole("dialog", { name: "Restart edelweiss?" });
    expect(
      within(d).getByText("The interface cannot stop this service: it would no longer run itself."),
    ).toBeTruthy();
    expect(
      within(d).getByText("The page is gone for a few seconds; after that you have to log in again."),
    ).toBeTruthy();
    expect(r.queryByRole("alert")).toBeNull();
    expect(api.calls).toEqual([]);
    fireEvent.click(r.getByRole("button", { name: "Cancel" }));
    expect(r.onClose).toHaveBeenCalledOnce();
  });

  it("speaks German, in the words of the preview", () => {
    routes();
    const r = renderDialog("de");
    const d = r.getByRole("dialog", { name: "edelweiss neu starten?" });
    expect(
      within(d).getByText(
        "Diesen Dienst kann die Oberfläche nicht stoppen – sie liefe sonst selbst nicht mehr.",
      ),
    ).toBeTruthy();
    expect(
      within(d).getByText("Die Seite ist ein paar Sekunden weg; danach musst du dich neu anmelden."),
    ).toBeTruthy();
    expect(r.getByRole("button", { name: "Abbrechen" })).toBeTruthy();
    expect(r.getByRole("button", { name: "Neu starten" }).className).toContain("btn-primary");
  });

  it("sends the restart, without a confirmation, and then shows the overlay instead of the dialog", async () => {
    const api = routes();
    const r = renderDialog();
    restart(r);
    // the button is off while the request is on its way
    expect((r.getByRole("button", { name: "Restart" }) as HTMLButtonElement).disabled).toBe(true);
    await settle();
    expect(api.sent("POST", "/jobs").map((c) => c.body)).toEqual([{ kind: "restart", service: "edelweiss" }]);
    expect(r.queryByRole("dialog")).toBeNull();
    expect(r.getByRole("status").textContent).toContain("edelweiss is restarting …");
    expect(r.onClose).not.toHaveBeenCalled();
  });

  it("shows the overlay in German", async () => {
    routes();
    const r = renderDialog("de");
    fireEvent.click(r.getByRole("button", { name: "Neu starten" }));
    await settle();
    expect(r.getByRole("status").textContent).toContain("edelweiss startet neu …");
  });

  it("does not ask for the job: it runs on while the extension goes away with edelweiss", async () => {
    const api = routes([noAnswer]);
    const r = renderDialog();
    restart(r);
    await settle();
    await vi.advanceTimersByTimeAsync(10 * RESTART_POLL_MS);
    expect(api.calls.filter((c) => c.path.startsWith("/jobs/"))).toEqual([]);
  });

  it("asks the core for the session every 2 seconds, and reloads the page once it answers after it was down", async () => {
    const api = routes([noAnswer, status(503), () => json({ user: { name: "alice" } })]);
    const r = renderDialog();
    restart(r);
    await settle();
    expect(RESTART_POLL_MS).toBe(2000);
    await vi.advanceTimersByTimeAsync(RESTART_POLL_MS - 1);
    expect(api.sent("GET", "/api/session")).toHaveLength(0);
    await vi.advanceTimersByTimeAsync(1);
    expect(api.sent("GET", "/api/session")).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(RESTART_POLL_MS);
    expect(api.sent("GET", "/api/session")).toHaveLength(2);
    expect(reload).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(RESTART_POLL_MS);
    expect(api.sent("GET", "/api/session")).toHaveLength(3);
    expect(reload).toHaveBeenCalledOnce();
    // and it asks no more
    await vi.advanceTimersByTimeAsync(5 * RESTART_POLL_MS);
    expect(api.sent("GET", "/api/session")).toHaveLength(3);
    expect(reload).toHaveBeenCalledOnce();
  });

  describe("the restart has to be seen before the page reloads", () => {
    // the old process answers until it stops: a page that reloaded then would land on a process that is about to go
    it("does not reload at the answers of the old process, only at the first one after the core was down", async () => {
      const api = routes([() => json({}), () => json({}), noAnswer, () => json({})]);
      const r = renderDialog();
      restart(r);
      await settle();
      await vi.advanceTimersByTimeAsync(3 * RESTART_POLL_MS);
      expect(api.sent("GET", "/api/session")).toHaveLength(3);
      expect(reload).not.toHaveBeenCalled();
      await vi.advanceTimersByTimeAsync(RESTART_POLL_MS);
      expect(api.sent("GET", "/api/session")).toHaveLength(4);
      expect(reload).toHaveBeenCalledOnce();
    });

    it("reloads at a 401, which the restart brings for everybody: all sessions are gone", async () => {
      const api = routes([
        () => json({}),
        () => json({ error: { code: "unauthenticated", message: "log in" } }, 401),
      ]);
      const r = renderDialog();
      restart(r);
      await settle();
      await vi.advanceTimersByTimeAsync(RESTART_POLL_MS);
      expect(reload).not.toHaveBeenCalled();
      await vi.advanceTimersByTimeAsync(RESTART_POLL_MS);
      expect(api.sent("GET", "/api/session")).toHaveLength(2);
      expect(reload).toHaveBeenCalledOnce();
    });

    it("reloads at a 401 that is no JSON as well", async () => {
      routes([() => new Response("<html>login</html>", { status: 401 })]);
      const r = renderDialog();
      restart(r);
      await settle();
      await vi.advanceTimersByTimeAsync(RESTART_POLL_MS);
      expect(reload).toHaveBeenCalledOnce();
    });

    it("reloads after 30 seconds when it has seen nothing, and not before", async () => {
      expect(RESTART_GIVE_UP_MS).toBe(30_000);
      const api = routes([() => json({})]);
      const r = renderDialog();
      restart(r);
      await settle();
      await vi.advanceTimersByTimeAsync(RESTART_GIVE_UP_MS - 1);
      expect(api.sent("GET", "/api/session")).toHaveLength(14);
      expect(reload).not.toHaveBeenCalled();
      await vi.advanceTimersByTimeAsync(1);
      expect(reload).toHaveBeenCalledOnce();
      // and it asks no more, and reloads once
      const asked = api.sent("GET", "/api/session").length;
      await vi.advanceTimersByTimeAsync(5 * RESTART_POLL_MS);
      expect(api.sent("GET", "/api/session")).toHaveLength(asked);
      expect(reload).toHaveBeenCalledOnce();
    });

    it("counts the 30 seconds from the accepted request, not from the click", async () => {
      let accept = () => {};
      const accepted = new Promise<void>((resolve) => {
        accept = resolve;
      });
      const api = stubApi({
        "POST /jobs": async () => {
          await accepted;
          return json(job({ id: "J9", kind: "restart", target: "edelweiss" }), 202);
        },
        "GET /api/session": () => json({}),
      });
      const r = renderDialog();
      restart(r);
      await vi.advanceTimersByTimeAsync(20_000);
      accept();
      await settle();
      await vi.advanceTimersByTimeAsync(RESTART_GIVE_UP_MS - 1);
      expect(reload).not.toHaveBeenCalled();
      await vi.advanceTimersByTimeAsync(1);
      expect(reload).toHaveBeenCalledOnce();
      expect(api.sent("POST", "/jobs")).toHaveLength(1);
    });

    // any other status of the old process (an error of its own) is no sign of the restart either
    it.each([
      [200, { user: { name: "alice" } }],
      [500, { error: { code: "failed", message: "boom" } }],
      [404, { error: { code: "not_found", message: "x" } }],
    ])(
      "does not reload at an answer with the status %i before the core was down, and does after",
      async (code, body) => {
        routes([() => json(body, code), () => json(body, code), noAnswer, () => json(body, code)]);
        const r = renderDialog();
        restart(r);
        await settle();
        await vi.advanceTimersByTimeAsync(3 * RESTART_POLL_MS);
        expect(reload).not.toHaveBeenCalled();
        await vi.advanceTimersByTimeAsync(RESTART_POLL_MS);
        expect(reload).toHaveBeenCalledOnce();
      },
    );

    it("takes an answer that is no JSON for an answer", async () => {
      routes([noAnswer, () => new Response("<html>hello</html>", { status: 200 })]);
      const r = renderDialog();
      restart(r);
      await settle();
      await vi.advanceTimersByTimeAsync(2 * RESTART_POLL_MS);
      expect(reload).toHaveBeenCalledOnce();
    });

    it("waits through a 502 and a 503, which a proxy answers while the service is down", async () => {
      const api = routes([status(502), status(503), status(502), status(200)]);
      const r = renderDialog();
      restart(r);
      await settle();
      await vi.advanceTimersByTimeAsync(3 * RESTART_POLL_MS);
      expect(api.sent("GET", "/api/session")).toHaveLength(3);
      expect(reload).not.toHaveBeenCalled();
      await vi.advanceTimersByTimeAsync(RESTART_POLL_MS);
      expect(reload).toHaveBeenCalledOnce();
    });

    it("counts a 502 or a 503 that comes after an answer as the core being down", async () => {
      routes([() => json({}), status(503), () => json({})]);
      const r = renderDialog();
      restart(r);
      await settle();
      await vi.advanceTimersByTimeAsync(2 * RESTART_POLL_MS);
      expect(reload).not.toHaveBeenCalled();
      await vi.advanceTimersByTimeAsync(RESTART_POLL_MS);
      expect(reload).toHaveBeenCalledOnce();
    });
  });

  it("stops asking when the dialog is gone", async () => {
    const api = routes([noAnswer]);
    const r = renderDialog();
    restart(r);
    await settle();
    await vi.advanceTimersByTimeAsync(RESTART_POLL_MS);
    expect(api.sent("GET", "/api/session")).toHaveLength(1);
    expect(vi.getTimerCount()).toBeGreaterThan(0); // the next poll and the 30 seconds
    r.unmount();
    expect(vi.getTimerCount()).toBe(0);
    await vi.advanceTimersByTimeAsync(2 * RESTART_GIVE_UP_MS);
    expect(api.sent("GET", "/api/session")).toHaveLength(1);
    expect(reload).not.toHaveBeenCalled();
  });

  // the request of a poll may be on its way when the dialog goes: whatever it brings later changes nothing
  it.each(["fails", "answers"] as const)(
    "does not go on when the dialog is gone while a request is on its way and then %s",
    async (outcome) => {
      let finish = () => {};
      const pending = new Promise<Response>((resolve, reject) => {
        finish = () =>
          outcome === "fails"
            ? reject(new TypeError("Failed to fetch"))
            : resolve(json({ user: { name: "alice" } }));
      });
      const api = routes([() => pending]);
      const r = renderDialog();
      restart(r);
      await settle();
      await vi.advanceTimersByTimeAsync(RESTART_POLL_MS);
      expect(api.sent("GET", "/api/session")).toHaveLength(1);
      r.unmount();
      finish();
      await settle();
      await vi.advanceTimersByTimeAsync(5 * RESTART_POLL_MS);
      expect(api.sent("GET", "/api/session")).toHaveLength(1);
      expect(reload).not.toHaveBeenCalled();
    },
  );

  it("does not ask before the request is accepted", async () => {
    const api = routes([() => json({})], () => apiError(409, "busy", "another change is running"));
    const r = renderDialog();
    restart(r);
    await settle();
    await vi.advanceTimersByTimeAsync(2 * RESTART_GIVE_UP_MS);
    expect(api.sent("GET", "/api/session")).toEqual([]);
    expect(reload).not.toHaveBeenCalled();
  });

  describe("a request that is refused", () => {
    it("shows why in the text of the code, keeps the dialog and can try again", async () => {
      const api = routes([() => json({})], () => apiError(409, "busy", "another change is running"));
      const r = renderDialog();
      restart(r);
      await settle();
      expect(r.getByRole("alert").textContent).toBe("Another change is running – try again in a moment.");
      expect(r.getByRole("dialog", { name: "Restart edelweiss?" })).toBeTruthy();
      expect(r.queryByRole("status")).toBeNull();
      expect(r.onReload).not.toHaveBeenCalled();
      expect((r.getByRole("button", { name: "Restart" }) as HTMLButtonElement).disabled).toBe(false);
      restart(r);
      await settle();
      expect(api.sent("POST", "/jobs")).toHaveLength(2);
    });

    it.each([
      [
        400,
        "invalid_input",
        "The service has changed in the meantime. Reload the list and try again.",
        { field: "kind" },
      ],
      [404, "not_found", "The service is gone – reload.", {}],
    ])(
      "reads the view again after %i %s, and leaves only Close: the request would be refused again",
      async (code, name, text, extra) => {
        const api = routes([() => json({})], () => apiError(code, name, "the message", extra));
        const r = renderDialog();
        restart(r);
        await settle();
        expect(r.onReload).toHaveBeenCalledOnce();
        expect(r.getByRole("alert").textContent).toBe(text);
        expect(r.queryByRole("button", { name: "Restart" })).toBeNull();
        expect(r.queryByRole("button", { name: "Cancel" })).toBeNull();
        fireEvent.click(r.getByRole("button", { name: "Close" }));
        expect(r.onClose).toHaveBeenCalledOnce();
        expect(api.sent("POST", "/jobs")).toHaveLength(1);
      },
    );

    it("leaves only Close in German too", async () => {
      routes([() => json({})], () => apiError(404, "not_found", "x"));
      const r = renderDialog("de");
      fireEvent.click(r.getByRole("button", { name: "Neu starten" }));
      await settle();
      expect(r.queryByRole("button", { name: "Neu starten" })).toBeNull();
      expect(r.getByRole("button", { name: "Schließen" })).toBeTruthy();
    });

    it("shows a request that fails without an answer", async () => {
      routes([() => json({})], noAnswer);
      const r = renderDialog();
      restart(r);
      await settle();
      expect(r.getByRole("alert").textContent).toBe("Failed to fetch");
      expect(r.queryByRole("status")).toBeNull();
    });
  });
});
