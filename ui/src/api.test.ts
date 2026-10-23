import { describe, expect, it } from "vitest";
import {
  autostartLevels,
  CHANGES,
  fits,
  goesDown,
  hasProblem,
  inAutostart,
  isSystem,
  type ServiceView,
  type State,
  servicePath,
} from "./api";
import { find, service, view } from "./test/fixtures";

describe("the autostart of a service", () => {
  it("is default and boot, in the order the service has them", () => {
    expect(autostartLevels({ runlevels: ["default"] })).toEqual(["default"]);
    expect(autostartLevels({ runlevels: ["boot", "default"] })).toEqual(["boot", "default"]);
  });

  it("leaves out every other runlevel", () => {
    expect(autostartLevels({ runlevels: ["nonetwork", "default", "sysinit"] })).toEqual(["default"]);
    expect(autostartLevels({ runlevels: [] })).toEqual([]);
  });

  it("is on for a service of default or boot only (api.inAutostart)", () => {
    expect(inAutostart({ runlevels: ["default"] })).toBe(true);
    expect(inAutostart({ runlevels: ["boot"] })).toBe(true);
    expect(inAutostart({ runlevels: ["nonetwork"] })).toBe(false);
    expect(inAutostart({ runlevels: [] })).toBe(false);
  });
});

describe("a system service", () => {
  it("is in sysinit or shutdown", () => {
    expect(isSystem({ runlevels: ["sysinit"] })).toBe(true);
    expect(isSystem({ runlevels: ["shutdown"] })).toBe(true);
    expect(isSystem({ runlevels: ["boot", "sysinit"] })).toBe(true);
  });

  it("is no service of boot, default or nonetwork", () => {
    expect(isSystem({ runlevels: ["boot"] })).toBe(false);
    expect(isSystem({ runlevels: ["default", "nonetwork"] })).toBe(false);
    expect(isSystem({ runlevels: [] })).toBe(false);
  });

  it("is what the view of the preview has as base services of a runlevel", () => {
    const v = view();
    expect(v.services.filter(isSystem).map((s) => s.name)).toEqual(["devfs", "killprocs"]);
    expect(find(v, "devfs").class).toBe("base");
  });
});

describe("a problem", () => {
  it("is a service that crashed or whose start failed", () => {
    expect(hasProblem({ state: "crashed" })).toBe(true);
    expect(hasProblem({ state: "failed" })).toBe(true);
    for (const state of ["started", "stopped", "starting", "stopping", "inactive"] as const)
      expect(hasProblem({ state }), state).toBe(false);
  });
});

describe("the kinds that change something", () => {
  it("are start, stop, restart, enable and disable, not the logs and the details", () => {
    expect([...CHANGES].sort()).toEqual(["disable", "enable", "restart", "start", "stop"]);
  });
});

describe("servicePath", () => {
  it("names the service below /services, escaped once", () => {
    expect(servicePath("docker")).toBe("/services/docker");
    expect(servicePath("net.eth0")).toBe("/services/net.eth0");
    expect(servicePath("a+b")).toBe("/services/a%2Bb");
  });
});

describe("a service that goes down with what it needs", () => {
  it("is started (a one-shot service too), starting, crashed or inactive (policy.goesDown)", () => {
    const yes: State[] = ["started", "starting", "crashed", "inactive"];
    for (const state of yes) expect(goesDown({ state }), state).toBe(true);
    const no: State[] = ["stopped", "stopping", "failed"];
    for (const state of no) expect(goesDown({ state }), state).toBe(false);
  });
});

describe("a change that fits the service", () => {
  const is = (state: State, over: Partial<ServiceView> = {}) => service({ name: "x", state, ...over });

  it("is a start of a service that is not started or starting (the start of a crashed one is a restart)", () => {
    for (const state of ["stopped", "failed", "crashed", "inactive", "stopping"] as const)
      expect(fits("start", is(state)), state).toBe(true);
    for (const state of ["started", "starting"] as const) expect(fits("start", is(state)), state).toBe(false);
  });

  it("is a stop of a service that is not stopped or stopping (failed is a stopped service whose start failed)", () => {
    for (const state of ["started", "starting", "crashed", "inactive"] as const)
      expect(fits("stop", is(state)), state).toBe(true);
    for (const state of ["stopped", "stopping", "failed"] as const)
      expect(fits("stop", is(state)), state).toBe(false);
  });

  it("is a restart of every service", () => {
    for (const state of [
      "started",
      "stopped",
      "crashed",
      "failed",
      "starting",
      "stopping",
      "inactive",
    ] as const)
      expect(fits("restart", is(state)), state).toBe(true);
  });

  it("is enable for a service that is in neither default nor boot, and disable for one that is in either", () => {
    for (const [runlevels, enable] of [
      [[], true],
      [["nonetwork"], true],
      [["default"], false],
      [["boot"], false],
      [["boot", "default"], false],
      [["default", "nonetwork"], false],
    ] as const) {
      expect(fits("enable", is("started", { runlevels: [...runlevels] })), runlevels.join()).toBe(enable);
      expect(fits("disable", is("started", { runlevels: [...runlevels] })), runlevels.join()).toBe(!enable);
    }
  });
});
