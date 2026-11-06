import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { app } from "../../content/app";
import { quarantineReasons } from "./types";

// The reason enum of the upload result in the core's spec, read from the file itself so a
// value added there without a text in the portal fails here instead of showing "No entró.".
function specReasons(): string[] {
  const yaml = readFileSync(new URL("../../../../core/api/openapi.yaml", import.meta.url), "utf8");
  const enums = [...yaml.matchAll(/^\s*enum: \[(unknown_layout,[^\]]*)\]/gm)];
  assert.equal(enums.length, 1, "exactly one quarantine reason enum in openapi.yaml");
  return enums[0][1].split(",").map((s) => s.trim());
}

test("the portal knows every quarantine reason of the spec, and no other", () => {
  assert.deepEqual([...quarantineReasons].sort(), specReasons().sort());
});

test("every quarantine reason has its own text, not the fallback", () => {
  for (const r of quarantineReasons) {
    const text = app.files.reasons[r];
    assert.ok(text && text.trim() !== "", `no text for ${r}`);
    assert.notEqual(text, app.files.otherReason, `${r} falls back to the generic text`);
  }
});
