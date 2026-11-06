// What an emailed link's refusal means (/ingresar and /invitacion), kept apart from the component
// so node --test can check it without rendering React.
import { ApiError } from "../../lib/api/errors";
import { app } from "../../content/app";

/** The token of an emailed link, from the URL fragment ("#token=…"), or null. */
export function tokenInFragment(hash: string): string | null {
  return new URLSearchParams(hash.replace(/^#/, "")).get("token") || null;
}

/**
 * F39: opening a second link in the same tab changes only the fragment, so the page is not
 * mounted again and would keep the first link's state with the new token left in the address
 * bar. This calls onNewLink whenever the fragment changes to one that carries a token, so the
 * page can start over (read and wipe the token, ask again). Returns the unsubscribe.
 */
export function watchTokenFragment(
  target: Pick<EventTarget, "addEventListener" | "removeEventListener">,
  readHash: () => string,
  onNewLink: () => void,
) {
  const changed = () => { if (tokenInFragment(readHash())) onNewLink(); };
  target.addEventListener("hashchange", changed);
  return () => target.removeEventListener("hashchange", changed);
}

/**
 * The message for an emailed link's refusal, per page. On /invitacion a 401 (used, expired or
 * replaced) and a 403 (cancelled) get the same message, true in every case (F42); on /ingresar
 * a 403 is the F36 one, for a person without access to that company today.
 */
export function explainToken(e: unknown, page: "invite" | "magic"): string {
  const s = app.session;
  const own = page === "invite" ? s.invite : s.magic;
  switch (tokenProblem(e)) {
    case "invalid": return own.invalid;
    case "not_member": return page === "invite" ? s.invite.invalid : s.magic.notMember;
    case "rate_limited": return e instanceof ApiError && e.retryAfter ? s.rateLimited(e.retryAfter) : s.rateLimitedNoWait;
    case "offline": return s.offline;
    default: return s.failed;
  }
}

/** What the page says when it opens with no token: a reload, since the token is wiped on read. */
export const noTokenMessage = (page: "invite" | "magic") => (page === "invite" ? app.session.invite.noToken : app.session.magic.noToken);

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
