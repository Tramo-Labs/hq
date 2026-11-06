// Sample mode's name rule must answer like the core's (internal/identity/name.go), including the
// edges where JavaScript and Go disagree on what to trim.
import { test } from "node:test";
import assert from "node:assert/strict";
import { login, updateName } from "./sample";
import { ApiError } from "./errors";

const cases: [string, string | null][] = [
  ["Ana", "Ana"],
  ["  Ana Pérez \n", "Ana Pérez"], // Go's TrimSpace also trims line breaks at the edges
  ["﻿Ana", null], // a BOM: trim() would drop it, Go keeps it and refuses it (Cf)
  ["Ana﻿", null],
  ["A​na", null], // zero width space (Cf)
  ["Ana‮Z", null], // bidi override (Cf)
  ["Ana\nPérez", null], // a line break inside (Cc)
  ["ㅤ", null], // Hangul filler, looks blank
  ["⠀", null], // Braille blank
  ["...", null], // no letter or number
  ["★", null], // a symbol alone
  ["x".repeat(120), "x".repeat(120)],
  ["x".repeat(121), null],
  ["   ", null],
];

test("updateName follows the core's name rules", async () => {
  await login("titular@agro.example", "ejemplo");
  for (const [input, want] of cases) {
    const label = JSON.stringify(input).slice(0, 24);
    if (want === null) {
      await assert.rejects(updateName(input), (e: unknown) => e instanceof ApiError && e.code === "invalid_name", `${label} should be refused`);
    } else {
      assert.equal(await updateName(input), want, `${label} should be stored as ${JSON.stringify(want).slice(0, 24)}`);
    }
  }
});
