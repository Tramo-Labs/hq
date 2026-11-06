// The /invitacion rules, kept apart from the component so node --test can check them without
// rendering React.
import { ApiError, SessionExpired } from "../../lib/api/errors";
import type { Role } from "../../lib/api/types";
import { tokenProblem, type TokenProblem } from "./token-rules";

/**
 * What the invitation page shows, given who is signed in on this browser: undefined while it is
 * still asking, null for nobody. Accepting without a session opens one, and the refresh cookie is
 * shared by every tab, so with someone in the page never accepts anonymously (F33). Since F41 it
 * first tries to accept for the account that is in; only if the invitation is for another account
 * does it show who is in and ask to sign out first.
 */
export type InviteStep = "checking" | "signed_in" | "form";

export function inviteStep(who: { email: string } | null | undefined): InviteStep {
  if (who === undefined) return "checking";
  return who === null ? "form" : "signed_in";
}

/**
 * D-19 (F26): one invitation link for everyone, and the page can not tell beforehand whether the
 * person already has an account. Without a session it accepts first without a password: an
 * existing account gets in with the link alone (the core never reads or changes its password).
 * Only a 400 password_required, which leaves the link usable, moves the page to the password step,
 * and only that step sends a password and a name.
 *
 * F41: who is in is asked again right before sending (F33). With a session, the accept goes with
 * that session's access token: the same account joins the company (204) and its session stays as
 * it is; another account gets 409 invite_for_another_account, nothing is spent, and the page shows
 * who is in and asks to sign out first. A session that ended meanwhile is treated as nobody in.
 */
export type InviteStage = "confirm" | "password";
export type InviteFields = { password: string; confirm: string; name: string };
export type InviteOutcome<W, S> =
  | { signedIn: W }
  | { joined: W }
  | { accepted: S }
  | { needsPassword: true }
  | { problem: PasswordProblem };

export async function acceptStep<W extends { email: string }, S>(
  stage: InviteStage,
  fields: InviteFields,
  who: () => Promise<W | null>,
  accept: (withPassword?: { password: string; name?: string }) => Promise<S>,
  acceptAs: (session: W) => Promise<void>,
): Promise<InviteOutcome<W, S>> {
  if (stage === "password") {
    const problem = passwordProblem(fields.password, fields.confirm);
    if (problem) return { problem };
  }
  const now = await who();
  if (now) {
    try {
      await acceptAs(now);
      return { joined: now };
    } catch (e) {
      if (e instanceof ApiError && e.code === "invite_for_another_account") return { signedIn: now };
      if (!(e instanceof SessionExpired)) throw e;
      // The session ended while sending: nobody is in any more, accept as without one.
    }
  }
  const body = stage === "password" ? { password: fields.password, name: fields.name.trim() || undefined } : undefined;
  try {
    return { accepted: await accept(body) };
  } catch (e) {
    if (stage === "confirm" && e instanceof ApiError && e.code === "password_required") return { needsPassword: true };
    throw e;
  }
}

/** The company the person just joined: the membership the session did not have before (F41). */
export function newMembership<M extends { tenantId: string }>(before: M[], after: M[]): M | null {
  const had = new Set(before.map((m) => m.tenantId));
  return after.find((m) => !had.has(m.tenantId)) ?? null;
}

/**
 * F38: before the accept button the page says which company and which role the invitation is
 * for. The preview is asked once, only when the page would offer the accept (first step, once it
 * knows who is in), since it shares the per-IP cap with accept. Accepting is offered only once the preview
 * answered: a dead link (401, 403) shows its message and no button; a passing problem (429,
 * offline) shows its message and a retry.
 */
export type PreviewState =
  | { status: "idle" }
  | { status: "loading" }
  | { status: "ready"; tenantName: string; role: Role }
  | { status: "failed"; problem: TokenProblem; retry: boolean };

export function needsPreview(step: InviteStep, stage: InviteStage, preview: PreviewState) {
  // F41: with someone in, the page also offers the accept (for that account), so it previews too.
  return step !== "checking" && stage === "confirm" && preview.status === "idle";
}

export function previewFailed(e: unknown): PreviewState {
  const problem = tokenProblem(e);
  return { status: "failed", problem, retry: problem !== "invalid" && problem !== "not_member" };
}

export const offersAccept = (preview: PreviewState) => preview.status === "ready";

export type PasswordProblem = "too_short" | "mismatch";

/**
 * The one problem that applies to what is typed now, or null (F34). It is worked out from the
 * current fields on every render, never kept from an earlier attempt: the browser blocks a short
 * password before the form is sent, so an error stored on submit would stay on screen.
 */
export function passwordProblem(password: string, confirm: string): PasswordProblem | null {
  if (password.length < 12) return "too_short";
  if (password !== confirm) return "mismatch";
  return null;
}
