"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useId, useRef, useState } from "react";
import { app } from "@/content/app";
import { acceptInvite, acceptMagicLink, ApiError, homeFor, logout, whoIsIn, type Session } from "@/lib/api";
import { acceptStep, inviteStep, passwordProblem, type InviteStage } from "@/components/app/invite-rules";
import { ghostBtn, primaryBtn } from "@/components/app/buttons";

const field = "min-h-11 w-full rounded-lg border border-white/20 bg-white/[0.04] px-3 text-[16px] text-white placeholder:text-white/50 focus:border-white/50 focus:outline-none";

/**
 * The token of an emailed link travels in the fragment (#token=…), which the browser never
 * sends to any server. It is read once and wiped from the address bar and history before
 * anything else, so it does not linger in the URL, in a screenshot or in a later share.
 */
export function readFragmentToken() {
  const token = new URLSearchParams(window.location.hash.slice(1)).get("token");
  if (window.location.hash) window.history.replaceState(null, "", window.location.pathname);
  return token;
}

/** Errors shared by the pages that take an emailed token. */
export function explainToken(e: unknown, invalid: string) {
  const s = app.session;
  if (e instanceof ApiError) {
    if (e.status === 401) return invalid;
    if (e.code === "not_a_member") return s.magic.notMember;
    if (e.status === 429) return e.retryAfter ? s.rateLimited(e.retryAfter) : s.rateLimitedNoWait;
    if (e.status >= 500) return s.offline;
  }
  return e instanceof TypeError ? s.offline : s.failed;
}

/**
 * /invitacion: the invited person accepts with the link; an existing account is in with that
 * alone, a person with no account then sets a name and a password (D-19). The token is read
 * and wiped from the URL on mount, then kept in memory until the invitation is accepted.
 */
export function InviteEntry() {
  const v = app.session.invite;
  const router = useRouter();
  const ids = { name: useId(), password: useId(), confirm: useId(), hint: useId(), error: useId() };
  const token = useRef<string | null | undefined>(undefined);
  const [missing, setMissing] = useState(false);
  // Who is signed in on this browser (F33): undefined while asking, null for nobody.
  const [who, setWho] = useState<Session | null | undefined>(undefined);
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  // The core's answer to the last attempt; cleared as soon as a field changes.
  const [error, setError] = useState<string | null>(null);
  // After the first attempt (sent, or stopped by the browser), the password problem is shown
  // as it is now, never as it was then (F34).
  const [tried, setTried] = useState(false);
  // D-19: accept with the link alone first; the password step only for a person with no account.
  const [stage, setStage] = useState<InviteStage>("confirm");

  useEffect(() => {
    let live = true;
    if (token.current === undefined) token.current = readFragmentToken();
    const absent = !token.current;
    Promise.resolve().then(() => { if (live && absent) setMissing(true); });
    if (!absent) whoIsIn().then((s) => { if (live) setWho(s); }, () => { if (live) setWho(null); });
    return () => { live = false; };
  }, []);

  const problem = tried ? passwordProblem(password, confirm) : null;
  const shown = problem === "too_short" ? v.tooShort : problem === "mismatch" ? v.mismatch : error;
  const edit = (set: (x: string) => void) => (e: React.ChangeEvent<HTMLInputElement>) => { set(e.target.value); setError(null); };

  async function signOutFirst() {
    if (busy) return;
    setBusy(true);
    try {
      await logout();
    } catch {
      // Whatever the core answered, ask again below: the form only opens if nobody is in.
    }
    try {
      setWho(await whoIsIn());
    } finally {
      setBusy(false);
    }
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (stage === "password") setTried(true);
    if (busy || !token.current || inviteStep(who) !== "form") return;
    if (stage === "password" && passwordProblem(password, confirm)) return;
    setBusy(true);
    setError(null);
    try {
      const t = token.current;
      const r = await acceptStep(stage, { password, confirm, name }, whoIsIn, (p) => acceptInvite(t, p?.password, p?.name));
      if ("signedIn" in r) {
        // Someone signed in in another tab while the form was open: back to who is in, no accept.
        setWho(r.signedIn);
        return;
      }
      if ("needsPassword" in r) {
        // No account yet (D-19): the link is still good; now ask for a name and a password.
        setStage("password");
        return;
      }
      if ("problem" in r) return;
      const s = r.accepted;
      setPassword("");
      setConfirm("");
      router.replace(homeFor(s.role));
    } catch (err) {
      // A 400 is either the name (invalid_name) or the request's validation, which here can only
      // be the password's length; each gets its own message.
      setError(err instanceof ApiError && err.code === "invalid_name" ? app.session.name.invalid
        : err instanceof ApiError && err.status === 400 ? v.rejected : explainToken(err, v.invalid));
    } finally {
      setBusy(false);
    }
  }

  if (missing) {
    return (
      <>
        <p className="font-mono text-[12px] tracking-wide text-white/60">{v.eyebrow}</p>
        <p role="alert" className="mt-4 text-[16px] leading-relaxed text-accent">{app.session.magic.noToken}</p>
        <Link href="/entrar" className={`${ghostBtn} mt-6`}>{app.session.magic.again}</Link>
      </>
    );
  }

  const step = inviteStep(who);
  if (step === "checking") {
    return (
      <>
        <p className="font-mono text-[12px] tracking-wide text-white/60">{v.eyebrow}</p>
        <p role="status" className="mt-4 text-[16px] text-white/80">{v.checking}</p>
      </>
    );
  }

  if (step === "signed_in" && who) {
    return (
      <>
        <p className="font-mono text-[12px] tracking-wide text-white/60">{v.eyebrow}</p>
        <h1 className="mt-3 text-[30px] font-light leading-[1.1] tracking-[-0.01em] sm:text-[40px]">{v.signedInTitle}</h1>
        <p className="mt-4 break-words text-[16px] leading-relaxed text-white/85">{v.signedIn(who.name ?? who.email, app.roles[who.role], who.tenantName)}</p>
        <p className="mt-3 text-[16px] leading-relaxed text-white/70">{v.signedInBody}</p>
        <div className="mt-8 flex flex-wrap gap-2">
          <button type="button" disabled={busy} aria-busy={busy} className={primaryBtn} onClick={signOutFirst}>{busy ? v.signingOut : v.signOut}</button>
          <Link href={homeFor(who.role)} className={ghostBtn}>{v.cancel}</Link>
        </div>
      </>
    );
  }

  if (stage === "confirm") {
    return (
      <>
        <p className="font-mono text-[12px] tracking-wide text-white/60">{v.eyebrow}</p>
        <h1 className="mt-3 text-[30px] font-light leading-[1.1] tracking-[-0.01em] sm:text-[40px]">{v.confirmTitle}</h1>
        <p className="mt-4 text-[16px] leading-relaxed text-white/70">{v.confirmBody}</p>
        <form onSubmit={submit} aria-describedby={error ? ids.error : undefined} className="mt-8 grid gap-5">
          {error && <p id={ids.error} role="alert" className="text-[14.5px] text-accent">{error}</p>}
          <button type="submit" disabled={busy} aria-busy={busy} className={`${primaryBtn} w-fit`}>{busy ? v.submitting : v.accept}</button>
        </form>
      </>
    );
  }

  return (
    <>
      <p className="font-mono text-[12px] tracking-wide text-white/60">{v.eyebrow}</p>
      <h1 className="mt-3 text-[30px] font-light leading-[1.1] tracking-[-0.01em] sm:text-[40px]">{v.title}</h1>
      <p className="mt-4 text-[16px] leading-relaxed text-white/70">{v.body}</p>
      <form onSubmit={submit} onInvalidCapture={() => setTried(true)} aria-describedby={shown ? ids.error : undefined} className="mt-8 grid gap-5">
        <div className="grid gap-1.5">
          <label htmlFor={ids.name} className="text-[14px] text-white/85">{v.name}</label>
          <input id={ids.name} name="name" autoComplete="name" maxLength={120} value={name} onChange={edit(setName)}
            placeholder={v.namePlaceholder} className={field} />
        </div>
        <div className="grid gap-1.5">
          <label htmlFor={ids.password} className="text-[14px] text-white/85">{v.password}</label>
          <input id={ids.password} type="password" name="new-password" autoComplete="new-password" required minLength={12} maxLength={128}
            aria-describedby={ids.hint} value={password} onChange={edit(setPassword)} className={field} />
          <p id={ids.hint} className="text-[13px] text-white/60">{v.passwordHint}</p>
        </div>
        <div className="grid gap-1.5">
          <label htmlFor={ids.confirm} className="text-[14px] text-white/85">{v.confirm}</label>
          <input id={ids.confirm} type="password" name="confirm-password" autoComplete="new-password" required minLength={12} maxLength={128}
            value={confirm} onChange={edit(setConfirm)} className={field} />
        </div>
        {shown && <p id={ids.error} role="alert" className="text-[14.5px] text-accent">{shown}</p>}
        <button type="submit" disabled={busy} aria-busy={busy} className={`${primaryBtn} w-fit`}>{busy ? v.submitting : v.submit}</button>
      </form>
    </>
  );
}

/** /ingresar: opens the session from a magic link, then goes to the person's home. */
export function MagicLinkEntry() {
  const m = app.session.magic;
  const router = useRouter();
  const [error, setError] = useState<string | null>(null);
  // The token is single use and wiped on read: one request per page, kept across React's
  // double mount in development (refs survive it), each mount only listening to the answer.
  const started = useRef<Promise<string | null> | null>(null);

  useEffect(() => {
    let live = true;
    started.current ??= (() => {
      const token = readFragmentToken();
      if (!token) return Promise.resolve(m.noToken);
      return acceptMagicLink(token).then((s) => { router.replace(homeFor(s.role)); return null; }, (e) => explainToken(e, m.invalid));
    })();
    started.current.then((msg) => { if (live && msg) setError(msg); });
    return () => { live = false; };
  }, [m, router]);

  return (
    <>
      <p className="font-mono text-[12px] tracking-wide text-white/60">{m.eyebrow}</p>
      {error ? (
        <>
          <p role="alert" className="mt-4 text-[16px] leading-relaxed text-accent">{error}</p>
          <Link href="/entrar" className={`${ghostBtn} mt-6`}>{m.again}</Link>
        </>
      ) : (
        <p role="status" className="mt-4 text-[16px] text-white/80">{m.opening}</p>
      )}
    </>
  );
}
