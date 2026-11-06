// F36: an emailed link answered 403 not_a_member, for a removed member and also for an
// invitation still pending. The message must be true in both cases.
import { test } from "node:test";
import assert from "node:assert/strict";
import { explainToken, noTokenMessage, tokenInFragment, tokenProblem, watchTokenFragment } from "./token-rules";

// F42: an invitation link that no longer works. 401 is used, expired or replaced by a newer
// invitation (the core does not say which, on purpose); 403 is a cancelled one.
test("a dead invitation link says it no longer works and to ask for a new one, for 401 and 403 alike", () => {
  const want = "Este enlace de invitación ya no sirve. Pedile a quien te invitó uno nuevo. Si ya tenés cuenta, también podés entrar con tu email.";
  for (const e of [new ApiError(401, "invalid_invite"), new ApiError(403, "not_a_member")]) assert.equal(explainToken(e, "invite"), want, String(e));
  // True in every case: it names no cause and sends nobody back to the mail they just used.
  assert.doesNotMatch(want, /se usó|venció|se canceló|aceptá la invitación|desde el mail/i);
});

test("the emailed sign in link keeps its own messages (F36)", () => {
  assert.equal(explainToken(new ApiError(401, "invalid_magic_link"), "magic"), app.session.magic.invalid);
  assert.equal(explainToken(new ApiError(403, "not_a_member"), "magic"), app.session.magic.notMember);
});

test("a reload says the link is read once, never that the mail came broken", () => {
  assert.equal(noTokenMessage("invite"), "Por seguridad, el enlace se lee una sola vez y no queda en la dirección. Para seguir, abrilo de nuevo desde el mail.");
  // A sign in link was already spent when the page read it: opening it again would not work.
  assert.equal(noTokenMessage("magic"), "Por seguridad, el enlace se lee una sola vez y no queda en la dirección. Si no llegaste a entrar, pedí uno nuevo.");
  for (const page of ["invite", "magic"] as const) assert.doesNotMatch(noTokenMessage(page), /incomplet/i, page);
});

test("the token is read from the fragment, and only from a token parameter", () => {
  assert.equal(tokenInFragment("#token=abc"), "abc");
  assert.equal(tokenInFragment("token=abc"), "abc");
  assert.equal(tokenInFragment("#x=1&token=abc"), "abc");
  assert.equal(tokenInFragment(""), null);
  assert.equal(tokenInFragment("#token="), null);
  assert.equal(tokenInFragment("#seccion"), null);
});

test("a second link in the same tab starts the page over; other fragment changes do not (F39)", () => {
  const tab = new EventTarget();
  let hash = "";
  let fresh = 0;
  const stop = watchTokenFragment(tab, () => hash, () => fresh++);
  hash = "#token=second";
  tab.dispatchEvent(new Event("hashchange"));
  assert.equal(fresh, 1, "a new link starts over");
  hash = "#seccion";
  tab.dispatchEvent(new Event("hashchange"));
  hash = "";
  tab.dispatchEvent(new Event("hashchange"));
  assert.equal(fresh, 1, "a fragment without a token changes nothing");
  hash = "#token=third";
  tab.dispatchEvent(new Event("popstate"));
  assert.equal(fresh, 1, "only the fragment change counts");
  tab.dispatchEvent(new Event("hashchange"));
  assert.equal(fresh, 2);
  stop();
  tab.dispatchEvent(new Event("hashchange"));
  assert.equal(fresh, 2, "after unmount it stops listening");
});
import { ApiError } from "../../lib/api/errors";
import { app } from "../../content/app";

test("each refusal of an emailed link gets its own meaning", () => {
  const cases: [unknown, string][] = [
    [new ApiError(401, "invalid_magic_link"), "invalid"],
    [new ApiError(401, "invalid_invite"), "invalid"],
    [new ApiError(403, "not_a_member"), "not_member"],
    [new ApiError(429, "rate_limited", undefined, 30), "rate_limited"],
    [new ApiError(503, "unavailable"), "offline"],
    [new TypeError("Failed to fetch"), "offline"],
    [new ApiError(400, "bad_request"), "failed"],
  ];
  for (const [e, want] of cases) assert.equal(tokenProblem(e), want, String(e));
});

test("the no-access messages never claim the person had access before (F36)", () => {
  // Pending invitations get the same 403 as removed members, so "ya no", "era para" or
  // "perdiste" would be false for someone who never got in.
  const past = /\bya no\b|\bera para\b|\bperdiste\b|\bdejaste\b/i;
  for (const [where, text] of [["link", app.session.magic.notMember], ["/entrar", app.session.notMember]] as const) {
    assert.doesNotMatch(text, past, where);
    assert.match(text, /invit/i, `${where} tells an invited person what to do`);
  }
});
