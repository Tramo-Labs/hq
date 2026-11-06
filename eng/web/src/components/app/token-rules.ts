// What an emailed link's refusal means (/ingresar and /invitacion), kept apart from the component
// so node --test can check it without rendering React.
import { ApiError } from "../../lib/api/errors";

export type TokenProblem = "invalid" | "not_member" | "rate_limited" | "offline" | "failed";

/**
 * 403 not_a_member says only that the person has no access to that company now: it comes for a
 * removed member and also for an invitation still pending (D-19, F36), so the message for it must
 * not claim the person ever had access.
 */
export function tokenProblem(e: unknown): TokenProblem {
  if (e instanceof ApiError) {
    if (e.status === 401) return "invalid";
    if (e.code === "not_a_member") return "not_member";
    if (e.status === 429) return "rate_limited";
    if (e.status >= 500) return "offline";
  }
  return e instanceof TypeError ? "offline" : "failed";
}
