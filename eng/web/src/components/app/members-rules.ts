// The Miembros rules, kept apart from the component so node --test can check them without
// rendering React. They mirror the core (internal/identity: CanGrant, CanRemoveMembers); the core
// enforces them, the screen only offers what it would accept.
import { ApiError } from "../../lib/api/errors";
import type { Member, Role } from "../../lib/api/types";

/**
 * The roles a person may give (spec §1 and §2, core CanGrant). An owner gives owner, approver,
 * viewer and accountant; an operator only the first owner; nobody gives operator here.
 */
export function grantableRoles(granter: Role): Role[] {
  if (granter === "owner") return ["owner", "approver", "viewer", "accountant"];
  if (granter === "operator") return ["owner"];
  return [];
}

/** Only an owner invites and removes people of the tenant (core CanRemoveMembers). */
export const managesMembers = (role: Role) => role === "owner";

/** Active people and pending invitations, shown apart (D-19). */
export function byStatus(items: Member[]) {
  return { active: items.filter((m) => m.status === "active"), pending: items.filter((m) => m.status === "pending") };
}

export type MemberProblem = "last_owner" | "already_member" | "invalid_email" | "forbidden" | "gone" | "offline" | "failed";

/** What went wrong inviting or removing, from the core's answer. */
export function memberProblem(e: unknown): MemberProblem {
  if (e instanceof ApiError) {
    if (e.code === "last_owner") return "last_owner";
    if (e.code === "already_member") return "already_member";
    if (e.code === "invalid_email" || e.status === 400 || e.status === 422) return "invalid_email";
    if (e.status === 403) return "forbidden";
    if (e.status === 404) return "gone";
    if (e.status >= 500) return "offline";
  }
  return e instanceof TypeError ? "offline" : "failed";
}
