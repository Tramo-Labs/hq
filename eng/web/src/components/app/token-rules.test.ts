// F36: an emailed link answered 403 not_a_member, for a removed member and also for an
// invitation still pending. The message must be true in both cases.
import { test } from "node:test";
import assert from "node:assert/strict";
import { tokenProblem } from "./token-rules";
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
