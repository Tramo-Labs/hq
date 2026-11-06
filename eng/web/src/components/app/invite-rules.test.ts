// F33 and F34 on /invitacion: the form only opens with nobody signed in, and the password
// message is the one that applies to what is typed now.
import { test } from "node:test";
import assert from "node:assert/strict";
import { acceptIfNobodyIn, acceptStep, inviteStep, passwordProblem } from "./invite-rules";
import { ApiError } from "../../lib/api/errors";

// D-19 (F26): one link for everyone; the page accepts with the link alone first.
const nobody = async () => null;
const blank = { password: "", confirm: "", name: "" };
function core(answers: (() => unknown)[]) {
  const calls: unknown[] = [];
  return {
    calls,
    accept: async (p?: { password: string; name?: string }) => {
      calls.push(p);
      return answers[calls.length - 1]();
    },
  };
}

test("an existing account accepts with the link alone: no password is ever sent (D-19)", async () => {
  const c = core([() => "session"]);
  assert.deepEqual(await acceptStep("confirm", blank, nobody, c.accept), { accepted: "session" });
  assert.deepEqual(c.calls, [undefined]);
});

test("a person with no account gets the password step only after 400 password_required (D-19)", async () => {
  const c = core([() => { throw new ApiError(400, "password_required"); }, () => "session"]);
  assert.deepEqual(await acceptStep("confirm", blank, nobody, c.accept), { needsPassword: true });
  const fields = { password: "x".repeat(12), confirm: "x".repeat(12), name: "  Vale Visor " };
  assert.deepEqual(await acceptStep("password", fields, nobody, c.accept), { accepted: "session" });
  assert.deepEqual(c.calls, [undefined, { password: "x".repeat(12), name: "Vale Visor" }]);
});

test("the password step checks the fields before sending anything", async () => {
  const c = core([]);
  assert.deepEqual(await acceptStep("password", { password: "123", confirm: "123", name: "" }, nobody, c.accept), { problem: "too_short" });
  assert.deepEqual(c.calls, []);
});

test("other answers on the first step are errors, not the password step", async () => {
  for (const e of [new ApiError(401, "invalid_invite"), new ApiError(403, "not_a_member"), new ApiError(400, "invalid_request")]) {
    const c = core([() => { throw e; }]);
    await assert.rejects(acceptStep("confirm", blank, nobody, c.accept), e);
  }
  // On the password step a password_required is an error too, never a loop back to it.
  const c = core([() => { throw new ApiError(400, "password_required"); }]);
  await assert.rejects(acceptStep("password", { password: "x".repeat(12), confirm: "x".repeat(12), name: "" }, nobody, c.accept));
});

test("both steps ask who is in right before sending (F33 stays)", async () => {
  const owner = async () => ({ email: "owner@alfa.test" });
  for (const stage of ["confirm", "password"] as const) {
    const c = core([() => "session"]);
    const r = await acceptStep(stage, { password: "x".repeat(12), confirm: "x".repeat(12), name: "" }, owner, c.accept);
    assert.deepEqual(r, { signedIn: { email: "owner@alfa.test" } }, stage);
    assert.deepEqual(c.calls, [], stage);
  }
});

test("the invitation form only shows when nobody is signed in on this browser (F33)", () => {
  assert.equal(inviteStep(undefined), "checking");
  assert.equal(inviteStep({ email: "owner@alfa.test" }), "signed_in");
  assert.equal(inviteStep(null), "form");
});

test("someone who signed in while the form was open stops the acceptance (F33)", async () => {
  let accepted = 0;
  const accept = async () => { accepted++; return "session of the invited person"; };
  const r = await acceptIfNobodyIn(async () => ({ email: "owner@alfa.test" }), accept);
  assert.deepEqual(r, { signedIn: { email: "owner@alfa.test" } });
  assert.equal(accepted, 0);
});

test("with nobody signed in just before sending, the invitation is accepted once (F33)", async () => {
  let accepted = 0;
  const r = await acceptIfNobodyIn(async () => null, async () => { accepted++; return "session"; });
  assert.deepEqual(r, { accepted: "session" });
  assert.equal(accepted, 1);
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
