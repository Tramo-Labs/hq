// The /entrar rules for "Entrar con un enlace por mail" (F32), kept apart from the component so
// node --test can check them without rendering React.

/** What the login form is showing, and how the last password attempt ended (null: none yet). */
export type LinkView = { signedIn: boolean; choosing: boolean; loginError: unknown };

/**
 * The link block shows whenever the form asks for credentials, whatever the last login answered:
 * an invited person with no password, or one who lost the invitation, has no other way in. The
 * core always answers 202, so offering it reveals nothing about which emails exist.
 */
export function showsLinkOffer(view: LinkView): boolean {
  return !view.signedIn && !view.choosing;
}

export type LinkResult = "need_email" | "sent";

/** Sends the link for the typed email, trimmed. With no email typed it sends nothing. */
export async function sendLinkFor(email: string, send: (email: string) => Promise<unknown>): Promise<LinkResult> {
  const typed = email.trim();
  if (!typed) return "need_email";
  await send(typed);
  return "sent";
}
