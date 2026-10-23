import { cleanup, fireEvent } from "@solidjs/testing-library";
import { afterEach, describe, expect, it } from "vitest";
import { find, service, view } from "./test/fixtures";
import { renderRow } from "./test/row";

afterEach(cleanup);

/** The tooltip over the switch: the title of the element around it. */
const titleOf = (sw: HTMLElement) => sw.closest("[title]")?.getAttribute("title") ?? undefined;

/** What the switch is described by (aria-describedby), as text. */
const descriptionOf = (sw: HTMLElement) => {
  const id = sw.getAttribute("aria-describedby");
  return id ? (document.getElementById(id)?.textContent ?? undefined) : undefined;
};

describe("ServiceRow", () => {
  describe("the switch of the autostart", () => {
    it("is on with the runlevel for a service of default or boot", () => {
      const r = renderRow({ name: "docker" });
      expect((r.getByRole("switch", { name: "Autostart docker" }) as HTMLInputElement).checked).toBe(true);
      expect(r.getByText("default")).toBeTruthy();
      cleanup();
      const boot = renderRow({ name: "networking", runlevels: ["boot"], class: "network" });
      expect((boot.getByRole("switch", { name: "Autostart networking" }) as HTMLInputElement).checked).toBe(
        true,
      );
      expect(boot.getByText("boot")).toBeTruthy();
    });

    it("is off for a service in no runlevel of the autostart", () => {
      const r = renderRow({ name: "samba", state: "stopped", runlevels: [] }, { locale: "de" });
      expect((r.getByRole("switch", { name: "Autostart samba" }) as HTMLInputElement).checked).toBe(false);
      expect(r.getByText("aus")).toBeTruthy();
      cleanup();
      const other = renderRow({ name: "x", runlevels: ["nonetwork"] });
      expect((other.getByRole("switch", { name: "Autostart x" }) as HTMLInputElement).checked).toBe(false);
      expect(other.getByText("off")).toBeTruthy();
    });

    it("asks to turn the autostart off, and leaves the switch as it is until the view changes", () => {
      const r = renderRow({ name: "docker" });
      const sw = r.getByRole("switch", { name: "Autostart docker" }) as HTMLInputElement;
      expect(titleOf(sw)).toBe("Turn the autostart off");
      expect(sw.hasAttribute("aria-describedby")).toBe(false);
      fireEvent.click(sw);
      expect(r.onAction).toHaveBeenCalledExactlyOnceWith("disable", r.service);
      expect(sw.checked).toBe(true);
    });

    it("asks to turn the autostart on, and leaves the switch as it is until the view changes", () => {
      const r = renderRow({ name: "samba", state: "stopped", runlevels: [] });
      const sw = r.getByRole("switch", { name: "Autostart samba" }) as HTMLInputElement;
      expect(titleOf(sw)).toBe("Turn the autostart on");
      fireEvent.click(sw);
      expect(r.onAction).toHaveBeenCalledExactlyOnceWith("enable", r.service);
      expect(sw.checked).toBe(false);
    });

    it("looks disabled, says why, and asks for nothing when the verdict is deny", () => {
      // edelweiss cannot leave the autostart
      const off = renderRow(find(view(), "edelweiss"), { locale: "de" });
      const locked = off.getByRole("switch", { name: "Autostart edelweiss" }) as HTMLInputElement;
      expect(locked.disabled).toBe(true);
      expect(locked.checked).toBe(true);
      expect(titleOf(locked)).toBe("kann nicht aus dem Autostart");
      fireEvent.click(locked);
      expect(off.onAction).not.toHaveBeenCalled();
      cleanup();
      // a service whose enable is denied, and that is not in the autostart
      const s = service({
        name: "x",
        runlevels: [],
        allowed: { start: "allow", restart: "allow", stop: "allow", enable: "deny", disable: "deny" },
      });
      const on = renderRow(s, { locale: "de" });
      const sw = on.getByRole("switch", { name: "Autostart x" }) as HTMLInputElement;
      expect(sw.disabled).toBe(true);
      expect(titleOf(sw)).toBe("kann nicht in den Autostart");
    });

    it("gives the reason on the wrapping element and to assistive technology, not on the disabled control", () => {
      const r = renderRow(find(view(), "edelweiss"), { locale: "de" });
      const sw = r.getByRole("switch", { name: "Autostart edelweiss" });
      // a disabled control shows no tooltip in some browsers: the title is on the element around it, with the text
      expect(sw.hasAttribute("title")).toBe(false);
      const wrapper = sw.closest("[title]") as HTMLElement;
      expect(wrapper).toBe(sw.parentElement);
      expect(wrapper.contains(r.getByText("default"))).toBe(true);
      expect(descriptionOf(sw)).toBe("kann nicht aus dem Autostart");
      // the name stays the name of the switch
      expect(sw.getAttribute("aria-label")).toBe("Autostart edelweiss");
    });

    it("looks disabled for a user who cannot change anything, and shows the state all the same", () => {
      const r = renderRow({ name: "docker" }, { readOnly: true });
      const sw = r.getByRole("switch", { name: "Autostart docker" }) as HTMLInputElement;
      expect(sw.disabled).toBe(true);
      expect(sw.checked).toBe(true);
      expect(r.getByText("default")).toBeTruthy();
      fireEvent.click(sw);
      expect(r.onAction).not.toHaveBeenCalled();
    });

    it("has no reason for a user who cannot change anything: the page says it once", () => {
      const r = renderRow(find(view(), "edelweiss"), { readOnly: true });
      const sw = r.getByRole("switch", { name: "Autostart edelweiss" });
      expect(titleOf(sw)).toBeUndefined();
      expect(descriptionOf(sw)).toBeUndefined();
    });

    it("is not there for a service of sysinit or shutdown, which shows its runlevel", () => {
      const r = renderRow(find(view(), "devfs"));
      expect(r.queryByRole("switch")).toBeNull();
      expect(r.getByText("sysinit")).toBeTruthy();
      cleanup();
      const down = renderRow(find(view(), "killprocs"));
      expect(down.queryByRole("switch")).toBeNull();
      expect(down.getByText("shutdown")).toBeTruthy();
    });
  });
});
