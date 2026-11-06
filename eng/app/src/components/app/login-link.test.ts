// F32: on /entrar the emailed link is offered from the start, not only after a 429 or a
// magic_link_required, and with no email typed it asks for one and sends nothing.
import { test } from "node:test";
import assert from "node:assert/strict";
import { sendLinkFor, showsLinkOffer } from "./login-link";
import { ApiError } from "../../lib/api/errors";

const form = { signedIn: false, choosing: false };

test("the link block shows on a clean /entrar, before any login attempt", () => {
  assert.equal(showsLinkOffer({ ...form, loginError: null }), true);
});

test("the link block shows after any failed login, not only a 429 or magic_link_required", () => {
  const answers = [
    new ApiError(401, "invalid_credentials"),
    new ApiError(429, "too_many_attempts", undefined, 30, false),
    new ApiError(429, "too_many_attempts", undefined, 30, true),
    new ApiError(401, "magic_link_required"),
    new ApiError(500, "unknown"),
    new TypeError("Failed to fetch"),
  ];
  for (const loginError of answers) assert.equal(showsLinkOffer({ ...form, loginError }), true, String(loginError));
});

test("the link block is not shown when the form does not ask for credentials", () => {
  assert.equal(showsLinkOffer({ signedIn: true, choosing: false, loginError: null }), false);
  assert.equal(showsLinkOffer({ signedIn: false, choosing: true, loginError: null }), false);
});

test("with no email typed it asks for the email and sends nothing", async () => {
  for (const email of ["", "   ", "\n\t "]) {
    const sent: string[] = [];
    const result = await sendLinkFor(email, async (e) => void sent.push(e));
    assert.equal(result, "need_email", JSON.stringify(email));
    assert.deepEqual(sent, [], JSON.stringify(email));
  }
});

test("with an email typed it sends the link once, for the trimmed email", async () => {
  const sent: string[] = [];
  assert.equal(await sendLinkFor("  ana@alfa.test ", async (e) => void sent.push(e)), "sent");
  assert.deepEqual(sent, ["ana@alfa.test"]);
});
