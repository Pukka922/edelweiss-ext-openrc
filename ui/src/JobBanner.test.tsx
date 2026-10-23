import { cleanup } from "@solidjs/testing-library";
import { afterEach, describe, expect, it } from "vitest";
import { JobBanner } from "./JobBanner";
import { job } from "./test/fixtures";
import { renderExt } from "./test/render";

afterEach(cleanup);

describe("JobBanner", () => {
  it("shows a running job with its kind, its target and its step", () => {
    const r = renderExt(() => (
      <JobBanner job={job({ kind: "restart", target: "nginx", steps: ["restart", "lbu"], step: 0 })} />
    ));
    expect(r.getByText("Running: Restart nginx")).toBeTruthy();
    expect(r.getByText("Step 1/2: restart the service")).toBeTruthy();
    expect(r.container.querySelector(".alert-info")).not.toBeNull();
    expect(r.container.querySelector(".loading")).not.toBeNull();
  });

  it("words the kinds of the autostart", () => {
    const r = renderExt(() => (
      <JobBanner job={job({ kind: "enable", target: "samba", steps: ["enable"] })} />
    ));
    expect(r.getByText("Running: Autostart on samba")).toBeTruthy();
  });

  it("shows an interrupted job as a warning with the step it stopped at", () => {
    const r = renderExt(() => (
      <JobBanner
        job={job({ status: "interrupted", kind: "stop", target: "docker", steps: ["stop", "lbu"], step: 1 })}
      />
    ));
    const alert = r.getByRole("alert");
    expect(alert.className).toContain("alert-warning");
    expect(alert.textContent).toBe(
      "Interrupted: Stop docker at step 2/2 (save the changes (lbu commit)). Check the service before you go on.",
    );
  });

  it("speaks German, and keeps a $ in the name of the service", () => {
    const r = renderExt(
      () => <JobBanner job={job({ kind: "start", target: "a$&b", steps: ["start"], step: 0 })} />,
      undefined,
      "de",
    );
    expect(r.getByText("Läuft: Starten a$&b")).toBeTruthy();
    expect(r.getByText("Schritt 1/1: Dienst starten")).toBeTruthy();
  });

  it("shows nothing for a job that is done or failed: it is no interruption", () => {
    for (const over of [
      { status: "done" as const },
      { status: "failed" as const, code: "failed", error: "rc-service nginx restart: exit status 1" },
    ]) {
      const r = renderExt(() => <JobBanner job={job(over)} />);
      expect(r.container.textContent, over.status).toBe("");
      expect(r.queryByRole("alert"), over.status).toBeNull();
      expect(r.container.querySelector(".alert"), over.status).toBeNull();
      cleanup();
    }
  });
});
