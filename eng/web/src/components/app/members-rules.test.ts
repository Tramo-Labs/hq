// Miembros: who may manage people, which roles they may give, active and pending apart, and
// what each refusal from the core means. The roles mirror the core's CanGrant.
import { test } from "node:test";
import assert from "node:assert/strict";
import { byStatus, grantableRoles, managesMembers, memberProblem } from "./members-rules";
import { ApiError } from "../../lib/api/errors";
import type { Member, Role } from "../../lib/api/types";

const roles: Role[] = ["owner", "approver", "viewer", "accountant", "operator"];

test("an owner gives owner, approver, viewer and accountant, never operator", () => {
  assert.deepEqual(grantableRoles("owner"), ["owner", "approver", "viewer", "accountant"]);
  assert.ok(!grantableRoles("owner").includes("operator"));
});

test("an operator gives only owner; the other roles give nothing", () => {
  assert.deepEqual(grantableRoles("operator"), ["owner"]);
  for (const r of ["approver", "viewer", "accountant"] as Role[]) assert.deepEqual(grantableRoles(r), [], r);
});

test("only an owner manages the members of the tenant", () => {
  for (const r of roles) assert.equal(managesMembers(r), r === "owner", r);
});

test("active people and pending invitations come apart, in the core's order", () => {
  const m = (email: string, status: Member["status"]): Member => ({ userId: email, email, name: null, role: "viewer", status });
  const items = [m("a@x.test", "active"), m("p1@x.test", "pending"), m("b@x.test", "active"), m("p2@x.test", "pending")];
  const { active, pending } = byStatus(items);
  assert.deepEqual(active.map((x) => x.email), ["a@x.test", "b@x.test"]);
  assert.deepEqual(pending.map((x) => x.email), ["p1@x.test", "p2@x.test"]);
});

test("each refusal from the core gets its own meaning", () => {
  const cases: [unknown, string][] = [
    [new ApiError(409, "last_owner"), "last_owner"],
    [new ApiError(409, "already_member"), "already_member"],
    [new ApiError(400, "invalid_email"), "invalid_email"],
    [new ApiError(422, "unprocessable"), "invalid_email"],
    [new ApiError(403, "forbidden"), "forbidden"],
    [new ApiError(404, "not_found"), "gone"],
    [new ApiError(503, "unavailable"), "offline"],
    [new TypeError("Failed to fetch"), "offline"],
    [new Error("?"), "failed"],
  ];
  for (const [e, want] of cases) assert.equal(memberProblem(e), want, String(e));
});
