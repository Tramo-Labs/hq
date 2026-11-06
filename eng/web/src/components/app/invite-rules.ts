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
