// /invitacion: who is in (F33, F41), the preview (F38), accepting with and without an account
// (D-19), and the password message (F34).
import { test } from "node:test";
import assert from "node:assert/strict";
import { acceptStep, inviteStep, needsPreview, newMembership, offersAccept, passwordProblem, previewFailed, type PreviewState } from "./invite-rules";
import { ApiError, SessionExpired } from "../../lib/api/errors";
import { app } from "../../content/app";

// F38: the company and the role before the accept button, asked once and only when it is offered.
const idle: PreviewState = { status: "idle" };
const ready: PreviewState = { status: "ready", tenantName: "Molino Alfa", role: "viewer" };

test("the preview is asked on the first step once it knows who is in, and only once (F38, F41)", () => {
  assert.equal(needsPreview("form", "confirm", idle), true);
  assert.equal(needsPreview("signed_in", "confirm", idle), true, "someone is in: the page offers the accept for that account");
  assert.equal(needsPreview("checking", "confirm", idle), false);
  assert.equal(needsPreview("form", "password", idle), false, "already past the preview");
  for (const p of [{ status: "loading" }, ready, { status: "failed", problem: "offline", retry: true }] as PreviewState[]) {
    assert.equal(needsPreview("form", "confirm", p), false, p.status);
  }
});

test("accepting is offered only once the preview said the company and role (F38)", () => {
  assert.equal(offersAccept(ready), true);
  for (const p of [idle, { status: "loading" }, previewFailed(new ApiError(429, "rate_limited"))] as PreviewState[]) assert.equal(offersAccept(p), false, p.status);
});

test("a dead link shows no retry; a passing problem does (F38)", () => {
  assert.deepEqual(previewFailed(new ApiError(401, "invalid_invite")), { status: "failed", problem: "invalid", retry: false });
  assert.deepEqual(previewFailed(new ApiError(403, "not_a_member")), { status: "failed", problem: "not_member", retry: false });
  assert.deepEqual(previewFailed(new ApiError(429, "rate_limited", undefined, 30)), { status: "failed", problem: "rate_limited", retry: true });
  assert.deepEqual(previewFailed(new TypeError("Failed to fetch")), { status: "failed", problem: "offline", retry: true });
});

test("who is in on this browser: asking, somebody, nobody", () => {
  assert.equal(inviteStep(undefined), "checking");
  assert.equal(inviteStep({ email: "owner@alfa.test" }), "signed_in");
  assert.equal(inviteStep(null), "form");
});

// A fake core: what the anonymous accept and the accept with the session's token answer.
const nobody = async () => null;
const blank = { password: "", confirm: "", name: "" };
const long = { password: "x".repeat(12), confirm: "x".repeat(12), name: "" };
type Who = { email: string };
function core(anon: (() => unknown)[], asAccount: (() => void)[] = []) {
  const calls: unknown[] = [];
  const asCalls: Who[] = [];
  return {
    calls,
    asCalls,
    accept: async (p?: { password: string; name?: string }) => {
      calls.push(p);
      return anon[calls.length - 1]();
    },
    acceptAs: async (w: Who) => {
      asCalls.push(w);
      asAccount[asCalls.length - 1]();
    },
  };
}

// D-19 (F26): one link for everyone; without a session the page accepts with the link alone first.
test("an existing account accepts with the link alone: no password is ever sent (D-19)", async () => {
  const c = core([() => "session"]);
  assert.deepEqual(await acceptStep("confirm", blank, nobody, c.accept, c.acceptAs), { accepted: "session" });
  assert.deepEqual(c.calls, [undefined]);
  assert.deepEqual(c.asCalls, []);
});

test("a person with no account gets the password step only after 400 password_required (D-19)", async () => {
  const c = core([() => { throw new ApiError(400, "password_required"); }, () => "session"]);
  assert.deepEqual(await acceptStep("confirm", blank, nobody, c.accept, c.acceptAs), { needsPassword: true });
  const fields = { password: "x".repeat(12), confirm: "x".repeat(12), name: "  Vale Visor " };
  assert.deepEqual(await acceptStep("password", fields, nobody, c.accept, c.acceptAs), { accepted: "session" });
  assert.deepEqual(c.calls, [undefined, { password: "x".repeat(12), name: "Vale Visor" }]);
});

test("the password step checks the fields before sending anything", async () => {
  const c = core([]);
  assert.deepEqual(await acceptStep("password", { password: "123", confirm: "123", name: "" }, nobody, c.accept, c.acceptAs), { problem: "too_short" });
  assert.deepEqual(c.calls, []);
});

test("other answers on the first step are errors, not the password step", async () => {
  for (const e of [new ApiError(401, "invalid_invite"), new ApiError(403, "not_a_member"), new ApiError(400, "invalid_request")]) {
    const c = core([() => { throw e; }]);
    await assert.rejects(acceptStep("confirm", blank, nobody, c.accept, c.acceptAs), e);
  }
  // On the password step a password_required is an error too, never a loop back to it.
  const c = core([() => { throw new ApiError(400, "password_required"); }]);
  await assert.rejects(acceptStep("password", long, nobody, c.accept, c.acceptAs));
});

// F41: with a session open the accept goes for that account, asked again right before sending.
const ana = { email: "ana@estudio.test" };

test("the same account as the invitation joins the company; nothing anonymous is sent (F41)", async () => {
  for (const stage of ["confirm", "password"] as const) {
    const c = core([], [() => {}]);
    assert.deepEqual(await acceptStep(stage, long, async () => ana, c.accept, c.acceptAs), { joined: ana }, stage);
    assert.deepEqual(c.asCalls, [ana], stage);
    assert.deepEqual(c.calls, [], `${stage}: the anonymous accept would open another session`);
  }
});

test("an invitation for another account shows who is in and spends nothing (F41, F33)", async () => {
  const c = core([], [() => { throw new ApiError(409, "invite_for_another_account"); }]);
  assert.deepEqual(await acceptStep("confirm", blank, async () => ana, c.accept, c.acceptAs), { signedIn: ana });
  assert.deepEqual(c.calls, []);
});

test("a session that ended while sending is treated as nobody in (F41)", async () => {
  const c = core([() => "session"], [() => { throw new SessionExpired(); }]);
  assert.deepEqual(await acceptStep("confirm", blank, async () => ana, c.accept, c.acceptAs), { accepted: "session" });
  assert.deepEqual(c.calls, [undefined]);
});

test("a dead link with a session open is an error, never a reason to accept anonymously (F41)", async () => {
  for (const e of [new ApiError(401, "invalid_invite"), new ApiError(403, "not_a_member")]) {
    const c = core([() => "session"], [() => { throw e; }]);
    await assert.rejects(acceptStep("confirm", blank, async () => ana, c.accept, c.acceptAs), e);
    assert.deepEqual(c.calls, []);
  }
});

test("before accepting with a session open, the page does not promise the invitation is for that account (F41)", () => {
  // Only the 409 invite_for_another_account tells; before the click nobody knows.
  const line = app.session.invite.withAccount("ana@estudio.test");
  assert.equal(line, "Si la invitación es para ana@estudio.test, se suma a tu cuenta sin cerrar tu sesión.");
  assert.doesNotMatch(line, /vas a aceptar|la aceptás|se va a sumar/i);
});

test("the company just joined is the membership the session did not have before (F41)", () => {
  const m = (tenantId: string) => ({ tenantId, tenantName: tenantId.toUpperCase(), role: "viewer" as const });
  assert.deepEqual(newMembership([m("alfa")], [m("alfa"), m("beta")]), m("beta"));
  assert.equal(newMembership([m("alfa"), m("beta")], [m("beta"), m("alfa")]), null);
});

test("the password message is the one that applies to the fields as they are now (F34)", () => {
  const long = "x".repeat(12);
  const cases: [string, string, ReturnType<typeof passwordProblem>][] = [
    ["123", "123", "too_short"], // F34: equal but short, not "no coinciden"
    ["123", "456", "too_short"],
    ["x".repeat(11), "x".repeat(11), "too_short"],
    [long, long + "y", "mismatch"],
    [long, "", "mismatch"],
    [long, long, null],
    ["x".repeat(128), "x".repeat(128), null],
  ];
  for (const [password, confirm, want] of cases) assert.equal(passwordProblem(password, confirm), want, `${password.length}/${confirm.length}`);
});

test("after a mismatch, fixing the fields to equal but short ones changes the message (F34 steps)", () => {
  assert.equal(passwordProblem("a".repeat(12), "b".repeat(12)), "mismatch");
  assert.equal(passwordProblem("123", "123"), "too_short");
  assert.equal(passwordProblem("c".repeat(12), "c".repeat(12)), null);
});
