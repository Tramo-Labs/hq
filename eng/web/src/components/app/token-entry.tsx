"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useId, useRef, useState } from "react";
import { app } from "@/content/app";
import { acceptInvite, acceptMagicLink, ApiError, homeFor } from "@/lib/api";
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
 * /invitacion: the invited person sets a name and a password, and is in. The token is read
 * and wiped from the URL on mount, then kept in memory until the form is sent.
 */
export function InviteEntry() {
  const v = app.session.invite;
  const router = useRouter();
  const ids = { name: useId(), password: useId(), confirm: useId(), hint: useId(), error: useId() };
  const token = useRef<string | null | undefined>(undefined);
  const [missing, setMissing] = useState(false);
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    if (token.current === undefined) token.current = readFragmentToken();
    const absent = !token.current;
    Promise.resolve().then(() => { if (live && absent) setMissing(true); });
    return () => { live = false; };
  }, []);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (busy || !token.current) return;
    if (password.length < 12) return setError(v.tooShort);
    if (password !== confirm) return setError(v.mismatch);
    setBusy(true);
    setError(null);
    try {
      const s = await acceptInvite(token.current, password, name.trim() || undefined);
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

  return (
    <>
      <p className="font-mono text-[12px] tracking-wide text-white/60">{v.eyebrow}</p>
      <h1 className="mt-3 text-[30px] font-light leading-[1.1] tracking-[-0.01em] sm:text-[40px]">{v.title}</h1>
      <p className="mt-4 text-[16px] leading-relaxed text-white/70">{v.body}</p>
      <form onSubmit={submit} aria-describedby={error ? ids.error : undefined} className="mt-8 grid gap-5">
        <div className="grid gap-1.5">
          <label htmlFor={ids.name} className="text-[14px] text-white/85">{v.name}</label>
          <input id={ids.name} name="name" autoComplete="name" maxLength={120} value={name} onChange={(e) => setName(e.target.value)}
            placeholder={v.namePlaceholder} className={field} />
        </div>
        <div className="grid gap-1.5">
          <label htmlFor={ids.password} className="text-[14px] text-white/85">{v.password}</label>
          <input id={ids.password} type="password" name="new-password" autoComplete="new-password" required minLength={12} maxLength={128}
            aria-describedby={ids.hint} value={password} onChange={(e) => setPassword(e.target.value)} className={field} />
          <p id={ids.hint} className="text-[13px] text-white/60">{v.passwordHint}</p>
        </div>
        <div className="grid gap-1.5">
          <label htmlFor={ids.confirm} className="text-[14px] text-white/85">{v.confirm}</label>
          <input id={ids.confirm} type="password" name="confirm-password" autoComplete="new-password" required minLength={12} maxLength={128}
            value={confirm} onChange={(e) => setConfirm(e.target.value)} className={field} />
        </div>
        {error && <p id={ids.error} role="alert" className="text-[14.5px] text-accent">{error}</p>}
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
