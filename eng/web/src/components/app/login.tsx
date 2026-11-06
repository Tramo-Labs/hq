"use client";

import { useRouter } from "next/navigation";
import { useEffect, useId, useState } from "react";
import { app } from "@/content/app";
import { ApiError, areaAllows, getMode, getSession, homeFor, login, logout, type Membership, type Session } from "@/lib/api";
import { primaryBtn, ghostBtn } from "@/components/app/buttons";

const s = app.session;
const field = "min-h-11 w-full rounded-lg border border-white/20 bg-white/[0.04] px-3 text-[16px] text-white placeholder:text-white/50 focus:border-white/50 focus:outline-none";

/** Only a path inside this site, so ?volver= cannot send anyone elsewhere. */
/**
 * Where to go after login: a path inside the person's own area, or their home. The path is
 * normalized with the URL parser first (dot segments, backslashes, protocol-relative forms), so
 * "/app/../../evil.example" is read as "/evil.example" and lands on the home, never elsewhere.
 */
const base = "http://tramo.invalid";
function safeBack(back: string | undefined, role: Session["role"]) {
  if (!back || !back.startsWith("/")) return homeFor(role);
  let u: URL;
  try {
    u = new URL(back, base);
  } catch {
    return homeFor(role);
  }
  if (u.origin !== base) return homeFor(role);
  const inside = (root: string) => u.pathname === root || u.pathname.startsWith(root + "/");
  const area = inside("/operador") ? "console" : inside("/app") ? "portal" : null;
  return area && areaAllows(area, role) ? u.pathname + u.search + u.hash : homeFor(role);
}

function explain(e: unknown) {
  if (e instanceof ApiError) {
    if (e.status === 401) return s.wrong;
    if (e.status === 429) return e.retryAfter ? s.rateLimited(e.retryAfter) : s.rateLimitedNoWait;
    if (e.status === 400) return s.invalid;
    if (e.status >= 500) return s.offline;
    return s.failed;
  }
  return e instanceof TypeError ? s.offline : s.failed;
}

function SampleAccounts() {
  const [list, setList] = useState<{ email: string; roles: string }[]>([]);
  useEffect(() => {
    import("@/lib/api/fixtures").then((fx) =>
      setList(fx.users.map((u) => ({ email: u.email, roles: [...new Set(u.memberships.map((m) => app.roles[m.role]))].join(", ") }))),
    );
  }, []);
  return (
    <div className="mt-8 rounded-xl border border-accent/30 bg-accent/10 p-4 text-[13.5px] text-accent">
      <p>{s.sampleHint}</p>
      <ul className="mt-2 grid gap-1 font-mono text-[12.5px]">
        {list.map((u) => <li key={u.email} className="break-all">{u.email} <span className="text-accent/80">· {u.roles}</span></li>)}
      </ul>
    </div>
  );
}

/**
 * /entrar. Email and password; with several memberships, the person picks the company.
 * The token is for one tenant, so picking another one logs in again with its tenantId
 * (the first session is revoked first). The password lives in this component's state
 * only while the form is open, and the access token only in memory (lib/api/session.ts).
 */
export function Login({ back, switching, tenantId, expired }: { back?: string; switching: boolean; tenantId?: string; expired: boolean }) {
  const router = useRouter();
  const ids = { email: useId(), password: useId(), error: useId() };
  const [current] = useState<Session | null>(() => (switching ? getSession() : null));
  const [email, setEmail] = useState(current?.email ?? "");
  const [password, setPassword] = useState("");
  const [choices, setChoices] = useState<Membership[] | null>(current && current.memberships.length > 1 ? current.memberships : null);
  const [chosen, setChosen] = useState(tenantId ?? current?.tenantId ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  function go(session: Session) {
    setPassword("");
    router.replace(safeBack(back, session.role));
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      if (current) {
        // Switching company: log in again for the chosen tenant (needs the password).
        go(await login(email.trim(), password, chosen));
        return;
      }
      if (choices) {
        const active = getSession();
        if (active && active.tenantId === chosen) return go(active);
        await logout();
        go(await login(email.trim(), password, chosen));
        return;
      }
      const session = await login(email.trim(), password, tenantId);
      if (!tenantId && session.memberships.length > 1) {
        setChoices(session.memberships);
        setChosen(session.tenantId);
        return;
      }
      go(session);
    } catch (err) {
      setError(explain(err));
    } finally {
      setBusy(false);
    }
  }

  const title = current ? s.switchTitle : choices ? s.chooseTitle : s.title;
  const body = current ? s.switchBody : choices ? s.chooseBody : s.body;
  const askCredentials = !choices || !!current;

  return (
    <>
      <p className="font-mono text-[12px] tracking-wide text-white/60">{s.eyebrow}</p>
      <h1 className="mt-3 text-[30px] font-light leading-[1.1] tracking-[-0.01em] sm:text-[40px]">{title}</h1>
      <p className="mt-4 text-[16px] leading-relaxed text-white/70">{body}</p>
      {expired && !current && <p role="status" className="mt-4 text-[14.5px] text-accent">{s.expired}</p>}

      <form onSubmit={submit} noValidate={false} aria-describedby={error ? ids.error : undefined} className="mt-8 grid gap-5">
        {choices && (
          <fieldset className="grid gap-2">
            <legend className="mb-2 text-[14px] text-white/85">{s.tenant}</legend>
            {choices.map((m) => (
              <label key={m.tenantId} className={`flex min-h-11 cursor-pointer items-center gap-3 rounded-lg border px-3 py-2 text-[15px] ${chosen === m.tenantId ? "border-white/60 bg-white/[0.06]" : "border-white/15"}`}>
                <input type="radio" name="tenant" value={m.tenantId} checked={chosen === m.tenantId} onChange={() => setChosen(m.tenantId)} className="size-4 accent-white" />
                <span className="min-w-0 flex-1 text-white">{m.tenantName}</span>
                <span className="font-mono text-[12px] text-white/60">{app.roles[m.role]}</span>
              </label>
            ))}
          </fieldset>
        )}
        {askCredentials && (
          <>
            <div className="grid gap-1.5">
              <label htmlFor={ids.email} className="text-[14px] text-white/85">{s.email}</label>
              <input id={ids.email} type="email" name="email" autoComplete="username" inputMode="email" required maxLength={254}
                readOnly={!!current} value={email} onChange={(e) => setEmail(e.target.value)} className={field} />
            </div>
            <div className="grid gap-1.5">
              <label htmlFor={ids.password} className="text-[14px] text-white/85">{s.password}</label>
              <input id={ids.password} type="password" name="password" autoComplete="current-password" required maxLength={128}
                value={password} onChange={(e) => setPassword(e.target.value)} className={field} />
            </div>
          </>
        )}
        {error && <p id={ids.error} role="alert" className="text-[14.5px] text-accent">{error}</p>}
        <div className="flex flex-wrap gap-2">
          <button type="submit" disabled={busy} aria-busy={busy} className={primaryBtn}>
            {busy ? s.submitting : current ? s.switch : choices ? s.choose : s.submit}
          </button>
          {current && <button type="button" className={ghostBtn} onClick={() => router.back()}>{s.cancel}</button>}
        </div>
      </form>

      {getMode() === "sample" && !current && <SampleAccounts />}
    </>
  );
}
