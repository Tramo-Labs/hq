import { test } from "node:test";
import assert from "node:assert/strict";
import { app } from "../../content/app";
import { accountText, rangeText, reasonText, truncatedNotice } from "./quarantine-rules";

test("a closed period reads as days of the month, the month once", () => {
  assert.equal(rangeText({ from: "2026-10-01", to: "2026-10-15" }), "del 1 al 15 de octubre");
  assert.equal(rangeText({ from: "2026-09-16", to: "2026-09-30" }), "del 16 al 30 de septiembre");
  assert.equal(rangeText({ from: "2026-02-16", to: "2026-02-28" }), "del 16 al 28 de febrero");
});

test("a range across months names both, and the year only across years", () => {
  assert.equal(rangeText({ from: "2026-10-16", to: "2026-11-02" }), "del 16 de octubre al 2 de noviembre");
  assert.equal(rangeText({ from: "2026-12-16", to: "2027-01-15" }), "del 16 de diciembre de 2026 al 15 de enero de 2027");
});

test("period_closed names the range the core sends", () => {
  const text = reasonText({ reason: "period_closed", closedPeriod: { from: "2026-10-01", to: "2026-10-15" } });
  assert.equal(text, app.files.periodClosed("del 1 al 15 de octubre"));
  assert.match(text, /del 1 al 15 de octubre/);
});

test("period_closed without a range keeps the text without one", () => {
  assert.equal(reasonText({ reason: "period_closed" }), app.files.reasons.period_closed);
});

test("other reasons keep their text, and an unknown or missing one the generic", () => {
  assert.equal(reasonText({ reason: "unrouted" }), app.files.reasons.unrouted);
  assert.equal(reasonText({ reason: "unrouted", closedPeriod: { from: "2026-10-01", to: "2026-10-15" } }), app.files.reasons.unrouted);
  assert.equal(reasonText({ reason: "something_new" }), app.files.otherReason);
  assert.equal(reasonText({}), app.files.otherReason);
});

test("a list cut at the core's limit says so, a whole one says nothing", () => {
  assert.equal(truncatedNotice({ truncated: true }), app.files.quarantineTruncated);
  assert.ok(app.files.quarantineTruncated.trim() !== "");
  assert.equal(truncatedNotice({ truncated: false }), null);
});

test("a file without an account says so instead of an empty name", () => {
  assert.equal(accountText({}), app.files.noAccount);
  assert.equal(accountText({ accountName: "" }), app.files.noAccount);
  assert.equal(accountText({ accountName: "Galicia CC" }), "Galicia CC");
});
