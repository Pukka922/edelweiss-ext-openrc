import { cleanup, fireEvent, waitFor, within } from "@solidjs/testing-library";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Details } from "../api";
import { details } from "../test/fixtures";
import { apiError, json, renderExt, stubApi } from "../test/render";
import { DetailsDialog } from "./DetailsDialog";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

/** The docker of the preview with its relations. */
const docker = (over: Partial<Details> = {}) =>
  details("docker", {
    needs: ["localmount", "net"],
    uses: ["logger"],
    after: ["dns", "firewall"],
    neededBy: ["portainer"],
    ...over,
  });

/** What the dialog says to a label: the text of the entry that follows it. */
const entry = (dialog: HTMLElement, label: string) => {
  const term = within(dialog).getByText(label, { selector: "dt" });
  return (term.nextElementSibling as HTMLElement).textContent;
};

function renderDetails(d: Details, opts: { locale?: "en" | "de"; name?: string; onClose?: () => void } = {}) {
  const name = opts.name ?? d.name;
  const api = stubApi({ [`GET /services/${encodeURIComponent(name)}`]: () => json(d) });
  const onClose = opts.onClose ?? vi.fn();
  const r = renderExt(() => <DetailsDialog name={name} onClose={onClose} />, undefined, opts.locale);
  return { ...r, api, onClose };
}

describe("DetailsDialog", () => {
  it("loads the service below /services and names it in the title", async () => {
    const r = renderDetails(docker());
    expect(r.getByRole("dialog", { name: "Details: docker" })).toBeTruthy();
    expect(r.getByRole("status")).toBeTruthy(); // loading
    await r.findByText("Docker Daemon");
    expect(r.api.sent("GET", "/services/docker")).toHaveLength(1);
    expect(r.queryByRole("status")).toBeNull();
  });

  it("asks for the name escaped once", async () => {
    const d = details("docker", { name: "a+b", script: "/etc/init.d/a+b" });
    const r = renderDetails(d, { name: "a+b" });
    await r.findByText("/etc/init.d/a+b");
    expect(r.api.sent("GET", "/services/a%2Bb")).toHaveLength(1);
  });

  it("shows the description, the status, the supervision, the autostart, the relations and the script", async () => {
    const r = renderDetails(docker());
    const dialog = await r.findByRole("dialog", { name: "Details: docker" });
    await within(dialog).findByText("Docker Daemon");
    expect(entry(dialog, "Description")).toBe("Docker Daemon");
    expect(entry(dialog, "Status")).toBe("running · uptime 03:12:09 · PID 2481");
    expect(entry(dialog, "Supervised")).toBe("yes (supervise-daemon, restarts after a crash)");
    expect(entry(dialog, "Autostart")).toBe("default");
    expect(entry(dialog, "Needs")).toBe("localmount, net");
    expect(entry(dialog, "Uses")).toBe("logger");
    expect(entry(dialog, "Starts after")).toBe("dns, firewall");
    expect(entry(dialog, "Needed by")).toBe("portainer");
    expect(entry(dialog, "Script")).toBe("/etc/init.d/docker");
    expect(within(dialog).getByText("/etc/init.d/docker").tagName).toBe("CODE");
  });

  it("speaks German", async () => {
    const r = renderDetails(docker(), { locale: "de" });
    const dialog = await r.findByRole("dialog", { name: "Details: docker" });
    await within(dialog).findByText("Docker Daemon");
    expect(entry(dialog, "Beschreibung")).toBe("Docker Daemon");
    expect(entry(dialog, "Status")).toBe("läuft · Laufzeit 03:12:09 · PID 2481");
    expect(entry(dialog, "Überwacht")).toBe("ja (supervise-daemon, startet nach Absturz neu)");
    expect(entry(dialog, "Autostart")).toBe("default");
    expect(entry(dialog, "Braucht")).toBe("localmount, net");
    expect(entry(dialog, "Nutzt")).toBe("logger");
    expect(entry(dialog, "Startet nach")).toBe("dns, firewall");
    expect(entry(dialog, "Gebraucht von")).toBe("portainer");
    expect(entry(dialog, "Skript")).toBe("/etc/init.d/docker");
    expect(within(dialog).getByRole("button", { name: "Schließen" })).toBeTruthy();
  });

  it("leaves out the uptime and the PID when they are not known, and shows a dash for what is empty", async () => {
    const d = details("samba", {
      state: "stopped",
      needs: [],
      uses: [],
      after: [],
      neededBy: [],
      runlevels: [],
    });
    const r = renderDetails(d, { locale: "de" });
    const dialog = await r.findByRole("dialog", { name: "Details: samba" });
    await within(dialog).findByText("SMB-Dateifreigaben");
    expect(entry(dialog, "Status")).toBe("gestoppt");
    expect(entry(dialog, "Überwacht")).toBe("nein");
    expect(entry(dialog, "Autostart")).toBe("aus");
    for (const label of ["Braucht", "Nutzt", "Startet nach", "Gebraucht von"])
      expect(entry(dialog, label), label).toBe("–");
  });

  it("names every runlevel the service is in, and a dash for no description", async () => {
    const d = details("networking", { description: "", runlevels: ["boot", "nonetwork"] });
    const r = renderDetails(d);
    const dialog = await r.findByRole("dialog", { name: "Details: networking" });
    await within(dialog).findByText("/etc/init.d/networking");
    expect(entry(dialog, "Autostart")).toBe("boot, nonetwork");
    expect(entry(dialog, "Description")).toBe("–");
    expect(entry(dialog, "Status")).toBe("started (one-shot)");
  });

  it("shows what the server sends as it is", async () => {
    const d = details("docker", { description: "Docker <b>$&</b> daemon", needs: ["a$'b"] });
    const r = renderDetails(d);
    const dialog = await r.findByRole("dialog", { name: "Details: docker" });
    await within(dialog).findByText("Docker <b>$&</b> daemon");
    expect(entry(dialog, "Needs")).toBe("a$'b");
  });

  it("says that the service is gone when the server does not have it", async () => {
    stubApi({ "GET /services/gone": () => apiError(404, "not_found", "there is no service gone") });
    const onClose = vi.fn();
    const r = renderExt(() => <DetailsDialog name="gone" onClose={onClose} />);
    expect((await r.findByRole("alert")).textContent).toContain("The service is gone – reload.");
    expect(r.queryByRole("status")).toBeNull();
    expect(r.queryByText("Script")).toBeNull();
    // the dialog still closes
    fireEvent.click(r.getByRole("button", { name: "Close" }));
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("shows the message of a failure of the host", async () => {
    stubApi({
      "GET /services/docker": () => apiError(500, "failed", "reading the relations of docker: boom"),
    });
    const r = renderExt(() => <DetailsDialog name="docker" onClose={() => {}} />);
    expect((await r.findByRole("alert")).textContent).toContain(
      "That failed: reading the relations of docker: boom",
    );
  });

  it("closes with its button and with Escape", async () => {
    const r = renderDetails(docker());
    await r.findByText("Docker Daemon");
    fireEvent.click(r.getByRole("button", { name: "Close" }));
    expect(r.onClose).toHaveBeenCalledTimes(1);
    fireEvent.keyDown(document, { key: "Escape" });
    await waitFor(() => expect(r.onClose).toHaveBeenCalledTimes(2));
  });

  it("closes with Escape while it loads", () => {
    const r = renderDetails(docker());
    fireEvent.keyDown(document, { key: "Escape" });
    expect(r.onClose).toHaveBeenCalledOnce();
  });
});
