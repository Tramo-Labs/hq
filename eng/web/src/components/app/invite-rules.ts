// The /invitacion rules, kept apart from the component so node --test can check them without
// rendering React.
import { ApiError } from "../../lib/api/errors";

/**
 * What the invitation page shows, given who is signed in on this browser: undefined while it is
 * still asking, null for nobody. Accepting replaces the browser's session (the refresh cookie is
 * shared by every tab), so the form only shows when nobody is in (F33). Whoever is in sees it
 * first and chooses: sign out and accept, or cancel.
 */
export type InviteStep = "checking" | "signed_in" | "form";

export function inviteStep(who: { email: string } | null | undefined): InviteStep {
  if (who === undefined) return "checking";
  return who === null ? "form" : "signed_in";
}

/**
 * Accepts only if nobody is signed in right now (F33). Someone may have signed in in another tab
 * while the form was open, so it asks again just before sending; if someone is in, it does not
 * accept and hands back who, for the page to show them.
 */
export async function acceptIfNobodyIn<W extends { email: string }, S>(
  who: () => Promise<W | null>,
  accept: () => Promise<S>,
): Promise<{ signedIn: W } | { accepted: S }> {
  const now = await who();
  if (inviteStep(now) !== "form") return { signedIn: now as W };
  return { accepted: await accept() };
}

/**
 * D-19 (F26): one invitation link for everyone, and the page can not tell beforehand whether the
 * person already has an account. So it accepts first without a password: an existing account gets
 * in with the link alone (the core never reads or changes its password). Only a 400
 * password_required, which leaves the link usable, moves the page to the password step, and only
 * that step sends a password and a name. Both sends go through the F33 re-check.
 */
export type InviteStage = "confirm" | "password";
export type InviteFields = { password: string; confirm: string; name: string };
export type InviteOutcome<W, S> = { signedIn: W } | { accepted: S } | { needsPassword: true } | { problem: PasswordProblem };

export async function acceptStep<W extends { email: string }, S>(
  stage: InviteStage,
  fields: InviteFields,
  who: () => Promise<W | null>,
  accept: (withPassword?: { password: string; name?: string }) => Promise<S>,
): Promise<InviteOutcome<W, S>> {
  if (stage === "password") {
    const problem = passwordProblem(fields.password, fields.confirm);
    if (problem) return { problem };
  }
  const body = stage === "password" ? { password: fields.password, name: fields.name.trim() || undefined } : undefined;
  try {
    return await acceptIfNobodyIn(who, () => accept(body));
  } catch (e) {
    if (stage === "confirm" && e instanceof ApiError && e.code === "password_required") return { needsPassword: true };
    throw e;
  }
}

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
