// F33 and F34 on /invitacion: the form only opens with nobody signed in, and the password
// message is the one that applies to what is typed now.
import { test } from "node:test";
import assert from "node:assert/strict";
import { acceptIfNobodyIn, inviteStep, passwordProblem } from "./invite-rules";

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
