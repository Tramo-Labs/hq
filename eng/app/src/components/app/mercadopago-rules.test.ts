import { test } from "node:test";
import assert from "node:assert/strict";
import { app } from "../../content/app";
import { mpActions, mpErrorText, mpState, readCallback, syncText } from "./mercadopago-rules";

const m = app.mercadoPago;
const conn = (over: Partial<Parameters<typeof syncText>[0]> = {}) => ({ configured: true, status: "disconnected" as const, ...over });

test("each connection reads as its own state", () => {
  assert.equal(mpState(conn()), "disconnected");
  assert.equal(mpState(conn({ status: "needs_reconnect" })), "needs_reconnect");
  assert.equal(mpState(conn({ status: "connected" })), "connected_no_import", "no lastImportAt: nothing imported yet");
  assert.equal(mpState(conn({ status: "connected", lastImportAt: "2026-11-06T09:05:00Z" })), "connected");
});

test("without the app's credentials it is unavailable, whatever the status", () => {
  for (const status of ["disconnected", "connected", "needs_reconnect"] as const) {
    assert.equal(mpState({ configured: false, status }), "unavailable", status);
    assert.deepEqual(mpActions({ configured: false, status }, "owner"), { connect: false, reconnect: false, disconnect: false }, `${status}: no button`);
  }
});

test("the owner gets the one button that fits the state", () => {
  assert.deepEqual(mpActions(conn(), "owner"), { connect: true, reconnect: false, disconnect: false });
  assert.deepEqual(mpActions(conn({ status: "needs_reconnect" }), "owner"), { connect: false, reconnect: true, disconnect: false });
  assert.deepEqual(mpActions(conn({ status: "connected" }), "owner"), { connect: false, reconnect: false, disconnect: true });
  assert.deepEqual(mpActions(conn({ status: "connected", lastImportAt: "2026-11-06T09:05:00Z" }), "owner"), { connect: false, reconnect: false, disconnect: true });
});

test("nobody but the owner connects, reconnects or disconnects", () => {
  for (const role of ["approver", "viewer", "accountant", "operator"] as const) {
    for (const status of ["disconnected", "connected", "needs_reconnect"] as const) {
      assert.deepEqual(mpActions(conn({ status }), role), { connect: false, reconnect: false, disconnect: false }, `${role} ${status}`);
    }
  }
});

test("the last sync shows only when it stopped, and only while connected", () => {
  const on = { status: "connected" as const };
  assert.equal(syncText(conn({ ...on, lastSyncCode: "stopped_by_quarantine" })), m.sync.stopped_by_quarantine);
  assert.equal(syncText(conn({ ...on, lastSyncCode: "mp_unavailable" })), m.sync.mp_unavailable);
  assert.equal(syncText(conn({ ...on, lastSyncCode: "imported" })), null, "imported: the import date already says it");
  assert.equal(syncText(conn(on)), null, "no sync yet");
  assert.equal(syncText(conn({ ...on, lastImportAt: "2026-11-06T09:05:00Z", lastSyncCode: "stopped_by_quarantine" })), m.sync.stopped_by_quarantine, "with an earlier import too");
  assert.equal(syncText(conn({ status: "needs_reconnect", lastSyncCode: "mp_unavailable" })), null, "not connected: the state line covers it");
  assert.equal(syncText({ configured: false, status: "connected", lastSyncCode: "mp_unavailable" }), null);
  assert.equal(syncText(conn({ ...on, lastSyncCode: "something_new" as never })), null, "an unknown code shows nothing");
});

test("every refusal of connect in the spec has its own text", () => {
  const codes = ["state_invalid", "state_not_yours", "state_expired", "state_used", "mp_code_rejected", "mp_unavailable", "not_configured"];
  const texts = new Set<string>();
  for (const code of codes) {
    const t = mpErrorText(code);
    assert.notEqual(t, m.errors.other, `${code} falls back to the generic text`);
    assert.ok(t.trim() !== "", code);
    texts.add(t);
  }
  assert.equal(texts.size, codes.length, "no two refusals share a text");
});

test("an unknown code gets the generic text, never Mercado Pago's own words", () => {
  assert.equal(mpErrorText("invalid_grant"), m.errors.other);
  assert.equal(mpErrorText("The authorization code has expired"), m.errors.other);
  assert.equal(mpErrorText(""), m.errors.other);
});

test("the return URL: code and state go to connect", () => {
  assert.deepEqual(readCallback("?code=TG-abc&state=s1"), { kind: "connect", code: "TG-abc", state: "s1" });
});

test("the return URL: an error is a refusal in Mercado Pago, whatever else it brings", () => {
  assert.deepEqual(readCallback("?error=access_denied&state=s1"), { kind: "cancelled" });
  assert.deepEqual(readCallback("?error=access_denied&error_description=User+denied&code=x&state=s1"), { kind: "cancelled" });
  assert.equal(mpErrorText(readCallback("?error=x").kind), m.errors.cancelled);
});

test("the return URL: without code or state there is nothing to send", () => {
  for (const q of ["", "?code=x", "?state=s1", "?code=&state=s1", "?code=x&state="]) {
    assert.deepEqual(readCallback(q), { kind: "missing" }, q || "(empty)");
  }
  assert.equal(mpErrorText("missing"), m.errors.missing);
});
