import { ApiError, createI18n } from "@edelweiss/sdk";
import { describe, expect, it } from "vitest";
import {
  agoText,
  errorText,
  jobErrorText,
  staleRefusal,
  stateText,
  stepLabel,
  stepText,
  tSafe,
  warnText,
} from "./errors";
import { job, service } from "./test/fixtures";
import { translator as openrc } from "./test/render";

/** The translator of these tests, with texts of their own: tSafe works on whatever the translator gives back. */
function translator(locale: "en" | "de") {
  const i18n = createI18n(locale);
  i18n.register("test", "en", {
    cancel: "Cancel",
    running: "{{ name }} is running.",
    runlevel: "{{ name }} is in the runlevel {{ level }}.",
    needs: "{{ name }} needs {{ other }}: without {{ other }} it does not start.",
  });
  i18n.register("test", "de", { running: "{{ name }} läuft." });
  return (key: string, params?: Record<string, string | number>) => i18n.translate("test", key, params);
}

describe("tSafe", () => {
  const t = translator("en");

  // a name or a message of the server may hold a "$": the patterns of String.replace must stay what they are
  it.each(["pc01$", "$&", "$'", "$`", "$$", "$1", "$<n>", "a$&b$'c", "pc01$' not found"])(
    "puts %s in unchanged",
    (value) => {
      expect(tSafe(t, "running", { name: value })).toBe(`${value} is running.`);
      expect(tSafe(t, "runlevel", { name: value, level: value })).toBe(
        `${value} is in the runlevel ${value}.`,
      );
    },
  );

  it("fills every placeholder, each as often as the text has it", () => {
    expect(tSafe(t, "runlevel", { name: "sshd", level: "default" })).toBe("sshd is in the runlevel default.");
    expect(tSafe(t, "needs", { name: "sshd", other: "networking" })).toBe(
      "sshd needs networking: without networking it does not start.",
    );
  });

  it("does not read a value as a placeholder, or as the mark of another one", () => {
    const text = tSafe(t, "runlevel", { name: "{{ level }}", level: "\u00000\u0000" });
    expect(text).toBe("{{ level }} is in the runlevel \u00000\u0000.");
    expect(tSafe(t, "runlevel", { name: "\u00001\u0000", level: "x" })).toBe(
      "\u00001\u0000 is in the runlevel x.",
    );
  });

  it("takes numbers and the empty string", () => {
    expect(tSafe(t, "runlevel", { name: 7, level: "" })).toBe("7 is in the runlevel .");
  });

  it("is the text without parameters when there are none, and the key when there is no text", () => {
    expect(tSafe(t, "cancel")).toBe("Cancel");
    expect(tSafe(t, "no.such.key", { name: "x" })).toBe("no.such.key");
  });

  it("speaks German too", () => {
    expect(tSafe(translator("de"), "running", { name: "pc01$" })).toBe("pc01$ läuft.");
  });
});

describe("errorText", () => {
  const en = openrc("en");
  const de = openrc("de");

  it("translates the codes of the API", () => {
    const cases: [number, string, string, string][] = [
      [403, "admin_required", "This needs admin mode.", "Dafür ist der Admin-Modus nötig."],
      [404, "not_found", "The service is gone – reload.", "Den Dienst gibt es nicht mehr – lade neu."],
      [409, "no_openrc", "OpenRC is not running on this system.", "Auf diesem System läuft kein OpenRC."],
      [409, "confirm_required", "This needs your confirmation.", "Das braucht deine Bestätigung."],
      [
        409,
        "busy",
        "Another change is running – try again in a moment.",
        "Gerade läuft schon eine andere Änderung – versuch es gleich noch einmal.",
      ],
    ];
    for (const [status, code, english, german] of cases) {
      const err = new ApiError(status, code, "the message of the server");
      expect(errorText(err, en), code).toBe(english);
      expect(errorText(err, de), code).toBe(german);
    }
  });

  it("puts the message of the server into the codes that carry it, unchanged", () => {
    const message = "rc-service: cannot read $' of $& in $$";
    const text = (code: string, t: typeof en) => errorText(new ApiError(500, code, message), t);
    expect(text("failed", en)).toBe(`That failed: ${message}`);
    expect(text("failed", de)).toBe(`Das ist fehlgeschlagen: ${message}`);
    expect(text("invalid_json", en)).toBe(`The request could not be read: ${message}`);
    expect(text("invalid_json", de)).toBe(`Die Anfrage war nicht lesbar: ${message}`);
    expect(text("invalid_input", en)).toBe(`The input is not valid: ${message}`);
    expect(text("invalid_input", de)).toBe(`Die Eingabe geht so nicht: ${message}`);
  });

  it("says that the service changed meanwhile for the state conflicts of the kind and the runlevel", () => {
    // the messages of internal/actions settle: the service is not as the request took it
    const conflicts: [string, string][] = [
      ["kind", "nginx is already started"],
      ["kind", "nginx is already stopped"],
      ["kind", "nginx is already in the runlevel default"],
      ["kind", "nginx is in neither default nor boot"],
      ["runlevel", "nginx is not in the runlevel boot"],
    ];
    for (const [field, message] of conflicts) {
      const err = new ApiError(400, "invalid_input", message, { field });
      expect(errorText(err, en), message).toBe(
        "The service has changed in the meantime. Reload the list and try again.",
      );
      expect(errorText(err, de), message).toBe(
        "Der Dienst hat sich inzwischen geändert. Lade die Liste neu und versuche es noch einmal.",
      );
    }
  });

  it("says what is wrong with the other fields of an invalid input", () => {
    const field = (name: string) => new ApiError(400, "invalid_input", "bad", { field: name });
    expect(errorText(field("service"), de)).toBe("Diesen Dienst gibt es nicht.");
    expect(errorText(field("service"), en)).toBe("There is no such service.");
    expect(errorText(field("offset"), en)).toBe("The position in the log is not valid. Open the log again.");
    expect(errorText(field("offset"), de)).toBe("Die Position im Log stimmt nicht. Öffne das Log neu.");
    // a field without a text: the message
    expect(errorText(field("nosuch"), en)).toBe("The input is not valid: bad");
    expect(errorText(new ApiError(400, "invalid_input", "bad"), de)).toBe("Die Eingabe geht so nicht: bad");
  });

  it("names the protected service and its class", () => {
    const edelweiss = new ApiError(409, "protected", "stop of edelweiss is not allowed (edelweiss)", {
      reason: "edelweiss",
      service: "edelweiss",
    });
    expect(errorText(edelweiss, en)).toBe(
      "edelweiss is protected (this interface) and cannot be changed that way.",
    );
    expect(errorText(edelweiss, de)).toBe(
      "edelweiss ist geschützt (diese Oberfläche) und lässt sich so nicht ändern.",
    );
    // a dependent that stops along is the protected one
    const blocker = new ApiError(409, "protected", "stop of docker would stop devfs", {
      reason: "base",
      service: "devfs",
    });
    expect(errorText(blocker, de)).toBe("devfs ist geschützt (Systembasis) und lässt sich so nicht ändern.");
  });

  it("names the protected service without a class when no class decided, and keeps a $ in the name", () => {
    const plain = new ApiError(409, "protected", "disable of samba is not allowed", { service: "samba" });
    expect(errorText(plain, de)).toBe("samba ist geschützt und lässt sich so nicht ändern.");
    expect(errorText(plain, en)).toBe("samba is protected and cannot be changed that way.");
    const odd = new ApiError(409, "protected", "x", { reason: "weird", service: "a$&b" });
    expect(errorText(odd, en)).toBe("a$&b is protected and cannot be changed that way.");
  });

  it("shows the message of the server for a protected refusal that names no service", () => {
    expect(errorText(new ApiError(409, "protected", "no way"), en)).toBe("no way");
  });

  it("shows the message of the server for a code without a text, and the message of any other error", () => {
    expect(errorText(new ApiError(500, "weird", "odd thing"), en)).toBe("odd thing");
    expect(errorText(new Error("network down"), en)).toBe("network down");
    expect(errorText("plain", en)).toBe("plain");
  });
});

describe("staleRefusal", () => {
  it("is an invalid input or a service that is not found: the page read the service before it changed", () => {
    expect(staleRefusal(new ApiError(400, "invalid_input", "x", { field: "kind" }))).toBe(true);
    expect(staleRefusal(new ApiError(404, "not_found", "x"))).toBe(true);
  });

  it("is no other refusal, and no error that is no answer of the server", () => {
    for (const [status, code] of [
      [409, "busy"],
      [409, "protected"],
      [409, "confirm_required"],
      [409, "no_openrc"],
      [403, "admin_required"],
      [400, "invalid_json"],
      [500, "failed"],
    ] as const)
      expect(staleRefusal(new ApiError(status, code, "x")), code).toBe(false);
    expect(staleRefusal(new TypeError("Failed to fetch"))).toBe(false);
    expect(staleRefusal("invalid_input")).toBe(false);
  });
});

describe("warnText", () => {
  const en = openrc("en");
  const de = openrc("de");
  const params = { name: "sshd", host: "edelweiss-pi" };

  it("words the warning of each class in German", () => {
    expect(warnText(de, "remote", params)).toBe(
      "sshd ist dein Fernzugang. Ohne sshd kommst du nicht mehr per SSH auf edelweiss-pi. Die Web-Oberfläche bleibt erreichbar.",
    );
    expect(warnText(de, "network", params)).toBe("Das betrifft das Netzwerk – damit auch diese Oberfläche.");
    expect(warnText(de, "firewall", params)).toBe("Neue Firewall-Regeln können dich aussperren.");
    expect(warnText(de, "storage", params)).toBe("Dateisysteme werden ausgehängt, RAIDs angehalten.");
  });

  it("words the warning of each class in English", () => {
    expect(warnText(en, "remote", params)).toBe(
      "sshd is your remote access. Without sshd you can no longer reach edelweiss-pi over SSH. The web interface stays available.",
    );
    expect(warnText(en, "network", params)).toBe("This affects the network – and with it this interface.");
    expect(warnText(en, "firewall", params)).toBe("New firewall rules can lock you out.");
    expect(warnText(en, "storage", params)).toBe("File systems are unmounted, RAID arrays are stopped.");
  });

  it("names the service of the way in, dropbear as well as sshd", () => {
    expect(warnText(de, "remote", { name: "dropbear", host: "edelweiss-pi" })).toBe(
      "dropbear ist dein Fernzugang. Ohne dropbear kommst du nicht mehr per SSH auf edelweiss-pi. Die Web-Oberfläche bleibt erreichbar.",
    );
    expect(warnText(en, "remote", { name: "dropbear", host: "edelweiss-pi" })).toBe(
      "dropbear is your remote access. Without dropbear you can no longer reach edelweiss-pi over SSH. The web interface stays available.",
    );
  });

  it("puts a host with a $ in unchanged", () => {
    expect(warnText(en, "remote", { name: "sshd", host: "pi$&x" })).toContain("reach pi$&x over SSH");
  });

  it("asks for the confirmation in plain words for a class without a warning of its own", () => {
    expect(warnText(en, "weird", params)).toBe("This needs your confirmation.");
    expect(warnText(de, "", params)).toBe("Das braucht deine Bestätigung.");
  });
});

describe("agoText", () => {
  const en = openrc("en");
  const de = openrc("de");
  const now = Date.parse("2026-10-20T08:00:00Z");
  const earlier = (seconds: number) => new Date(now - seconds * 1000).toISOString();

  it("says how long ago a time was, in whole minutes, hours and days, as the preview does", () => {
    const cases: [number, string, string][] = [
      [0, "just now", "gerade eben"],
      [59, "just now", "gerade eben"],
      [60, "1 min ago", "vor 1 min"],
      [4 * 60 + 59, "4 min ago", "vor 4 min"],
      [59 * 60 + 59, "59 min ago", "vor 59 min"],
      [3600, "1 h ago", "vor 1 h"],
      [23 * 3600 + 3599, "23 h ago", "vor 23 h"],
      [86400, "1 day ago", "vor 1 Tag"],
      [3 * 86400 + 5, "3 days ago", "vor 3 Tagen"],
    ];
    for (const [seconds, english, german] of cases) {
      expect(agoText(en, earlier(seconds), now), `${seconds} s`).toBe(english);
      expect(agoText(de, earlier(seconds), now), `${seconds} s`).toBe(german);
    }
  });

  it("takes a time a little after now (the clock of the browser is behind) as just now", () => {
    expect(agoText(de, earlier(-30), now)).toBe("gerade eben");
  });

  it("has nothing for a time it cannot read", () => {
    expect(agoText(en, "yesterday", now)).toBeUndefined();
    expect(agoText(en, "", now)).toBeUndefined();
  });
});

describe("stateText", () => {
  const en = openrc("en");
  const de = openrc("de");
  type Over = Parameters<typeof service>[0];
  const text = (t: typeof en, over: Over) => stateText(t, service(over));

  it("says what the state is", () => {
    const cases: [Over, string, string][] = [
      [{ name: "a", state: "started" }, "running", "läuft"],
      [{ name: "a", state: "stopped" }, "stopped", "gestoppt"],
      [{ name: "a", state: "crashed" }, "crashed", "abgestürzt"],
      [{ name: "a", state: "failed" }, "start failed", "Start fehlgeschlagen"],
      [{ name: "a", state: "starting" }, "starting …", "startet …"],
      [{ name: "a", state: "stopping" }, "stopping …", "stoppt …"],
      [{ name: "a", state: "inactive" }, "inactive", "inaktiv"],
    ];
    for (const [over, english, german] of cases) {
      expect(text(en, over), over.state).toBe(english);
      expect(text(de, over), over.state).toBe(german);
    }
  });

  it("calls a started service without a daemon a one-shot", () => {
    expect(text(de, { name: "a", oneShot: true })).toBe("gestartet (einmalig)");
    expect(text(en, { name: "a", oneShot: true })).toBe("started (one-shot)");
  });

  it("says that a stopped service of the autostart does not run, and only for a stopped one", () => {
    expect(text(de, { name: "a", state: "stopped", autostartIdle: true })).toBe("im Autostart, läuft nicht");
    expect(text(en, { name: "a", state: "stopped", autostartIdle: true })).toBe(
      "in the autostart, not running",
    );
    expect(text(de, { name: "a", state: "crashed", autostartIdle: true })).toBe("abgestürzt");
    expect(text(de, { name: "a", state: "stopped", oneShot: true })).toBe("gestoppt");
  });

  it("shows a state it does not know as it is", () => {
    expect(stateText(en, { state: "scheduled" as never, oneShot: false, autostartIdle: false })).toBe(
      "scheduled",
    );
  });
});

describe("the steps of a job", () => {
  const en = openrc("en");

  it("words the label of a step, and keeps one without a text", () => {
    expect(stepLabel(en, "restart")).toBe("restart the service");
    expect(stepLabel(openrc("de"), "lbu")).toBe("Änderungen sichern (lbu commit)");
    expect(stepLabel(en, "odd")).toBe("odd");
    expect(stepLabel(en, undefined)).toBe("");
  });

  it("names the step a job is at", () => {
    expect(stepText(job({ steps: ["disable", "lbu"], step: 1 }), en)).toBe(
      "Step 2/2: save the changes (lbu commit)",
    );
    expect(stepText(job({ steps: ["start"], step: 0 }), openrc("de"))).toBe("Schritt 1/1: Dienst starten");
  });
});

describe("jobErrorText", () => {
  it("names the step and the message of a failure, unchanged", () => {
    const st = job({
      status: "failed",
      code: "failed",
      step: 0,
      error: "rc-service nginx start: exit 1 ($&)",
    });
    expect(jobErrorText(st, openrc("en"))).toBe(
      "Step 1/2 (restart the service) failed: rc-service nginx start: exit 1 ($&)",
    );
    expect(jobErrorText(st, openrc("de"))).toBe(
      "Schritt 1/2 (Dienst neu starten) ist fehlgeschlagen: rc-service nginx start: exit 1 ($&)",
    );
  });

  it("takes the text of a failure for a code without one", () => {
    const st = job({ status: "failed", code: "odd", step: 1, error: "lbu: exit status 1" });
    expect(jobErrorText(st, openrc("en"))).toBe(
      "Step 2/2 (save the changes (lbu commit)) failed: lbu: exit status 1",
    );
  });

  it("says where an interrupted job stopped", () => {
    const st = job({ status: "interrupted", code: "interrupted", step: 0 });
    expect(jobErrorText(st, openrc("de"))).toBe(
      "Der Vorgang wurde bei Schritt 1/2 (Dienst neu starten) unterbrochen. Prüfe den Zustand des Dienstes, bevor du weitermachst.",
    );
  });
});
