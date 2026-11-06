// The /invitacion rules, kept apart from the component so node --test can check them without
// rendering React.

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
