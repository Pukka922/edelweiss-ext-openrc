import { expect, it } from "vitest";
import de from "../public/locales/de.json";
import en from "../public/locales/en.json";

const keys = (o: object, prefix = ""): string[] =>
  Object.entries(o).flatMap(([k, v]) =>
    typeof v === "object" && v !== null ? keys(v, `${prefix}${k}.`) : [`${prefix}${k}`],
  );

it("has the same keys in English and German", () => {
  expect(keys(de).sort()).toEqual(keys(en).sort());
});
