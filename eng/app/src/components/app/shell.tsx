"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useId, useState, type ReactNode } from "react";
import { brand } from "@tramo/ui/brand";
import { app } from "@/content/app";
import { ApiError, areaAllows, getMode, homeFor, updateName, type Session } from "@/lib/api";
import { ghostBtn, primaryBtn } from "@/components/app/buttons";
import { SessionGate, useSignOut } from "@/components/app/session";
import { Notice } from "@/components/app/ui";
import { managesMembers } from "@/components/app/members-rules";

type Area = "portal" | "console";

function NavLinks({ items }: { items: { label: string; href: string }[] }) {
  const path = usePathname();
  return (
    <nav className="flex w-full flex-wrap gap-1 sm:w-auto">
      {items.map((n) => {
        const active = path === n.href;
        return (
          <Link key={n.href} href={n.href} aria-current={active ? "page" : undefined}
            className={`inline-flex min-h-11 items-center rounded-full px-3 text-[14px] ${active ? "bg-white/10 text-white" : "text-white/70 hover:text-white"}`}>
            {n.label}
          </Link>
        );
      })}
    </nav>
  );
}

function Account({ session }: { session: Session }) {
  const s = app.session;
  const path = usePathname();
  const signOut = useSignOut();
  const [leaving, setLeaving] = useState(false);
  const [naming, setNaming] = useState(false);
  const nameId = useId();
  function out() {
    setLeaving(true);
    signOut();
  }
  return (
    <div className="flex w-full flex-wrap items-center gap-x-3 gap-y-1 text-[13px] text-white/70 sm:w-auto">
      <span className="min-w-0 break-all">{s.signedAs(session.name ?? session.email, app.roles[session.role])}</span>
      <span className="text-white">{session.tenantName}</span>
      <button type="button" onClick={() => setNaming((v) => !v)} aria-expanded={naming} aria-controls={nameId}
        className="inline-flex min-h-11 items-center underline decoration-white/40 underline-offset-4 hover:text-white">
        {s.name.open}
      </button>
      {session.memberships.length > 1 && (
        <Link href={`/entrar?cambiar=1&volver=${encodeURIComponent(path)}`} className="inline-flex min-h-11 items-center underline decoration-white/40 underline-offset-4 hover:text-white">
          {s.switchTitle}
        </Link>
      )}
      <button type="button" onClick={out} disabled={leaving} aria-busy={leaving}
        className="inline-flex min-h-11 items-center rounded-full border border-white/25 px-3 text-paper hover:bg-white/10 disabled:opacity-60">
        {leaving ? s.loggingOut : s.logout}
      </button>
      {naming && <NameForm id={nameId} current={session.name} onDone={() => setNaming(false)} />}
    </div>
  );
}

/** "Tu nombre": PATCH /v1/me with the person's own name; the header shows it at once. */
function NameForm({ id, current, onDone }: { id: string; current: string | null; onDone: () => void }) {
  const n = app.session.name;
  const inputId = useId();
  const [value, setValue] = useState(current ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  async function save(e: React.FormEvent) {
    e.preventDefault();
    if (busy) return;
    if (!value.trim()) return setError(n.invalid);
    setBusy(true);
    setError(null);
    try {
      await updateName(value);
      onDone();
    } catch (err) {
      setError(err instanceof ApiError && err.status === 400 ? n.invalid : n.failed);
    } finally {
      setBusy(false);
    }
  }
  return (
    <form id={id} onSubmit={save} className="grid w-full gap-2 py-2">
      <label htmlFor={inputId} className="text-[13px] text-white/85">{n.label}</label>
      <div className="flex flex-wrap gap-2">
        <input id={inputId} autoComplete="name" maxLength={120} value={value} onChange={(e) => setValue(e.target.value)} placeholder={n.placeholder}
          className="min-h-11 min-w-0 flex-1 rounded-lg border border-white/20 bg-white/[0.04] px-3 text-[16px] text-white placeholder:text-white/50 focus:border-white/50 focus:outline-none" />
        <button type="submit" disabled={busy} aria-busy={busy} className={primaryBtn}>{n.save}</button>
        <button type="button" disabled={busy} className={ghostBtn} onClick={onDone}>{app.approvals.cancel}</button>
      </div>
      {error && <p role="alert" className="text-[13.5px] text-accent">{error}</p>}
    </form>
  );
}

/** Header, nav, session and the sample-data notice shared by the portal and the console. */
export function Shell({ area, children }: { area: Area; children: ReactNode }) {
  const copy = area === "portal" ? app.portal : app.console;
  const loading = <Notice>{app.load.loading}</Notice>;
  return (
    <div className="min-h-dvh">
      <a href="#contenido" className="sr-only rounded bg-paper px-4 text-ink focus:not-sr-only focus:fixed focus:left-4 focus:top-4 focus:z-50 focus:inline-flex focus:min-h-11 focus:items-center">{app.skip}</a>
      <SessionGate area={area} loading={<Frame area={area} copy={copy}>{loading}</Frame>}>
        {(session) => (
          <Frame area={area} copy={copy} session={session}>
            {areaAllows(area, session.role) ? children : (
              <Notice>
                {app.session.wrongArea[area]}{" "}
                <Link href={homeFor(session.role)} className="inline-flex min-h-11 items-center text-white underline decoration-white/40 underline-offset-4">
                  {app.session.wrongArea.go[area]}
                </Link>
              </Notice>
            )}
          </Frame>
        )}
      </SessionGate>
    </div>
  );
}

function Frame({ area, copy, session, children }: { area: Area; copy: typeof app.portal; session?: Session; children: ReactNode }) {
  return (
    <>
      <header className="sticky top-0 z-20 border-b border-white/10 bg-ink/90 backdrop-blur">
        <div className="mx-auto flex max-w-5xl flex-wrap items-center justify-between gap-x-6 gap-y-2 px-4 py-3">
          <p className="flex items-center gap-2">
            <Link href="/" className="inline-flex min-h-11 items-center text-[17px] font-medium tracking-tight">{brand.name}</Link>
            <span className="font-mono text-[12px] text-white/60">{copy.label}</span>
          </p>
          {session && <Account session={session} />}
          {session && areaAllows(area, session.role) && (
            <NavLinks items={copy.nav.filter((n) => !("onlyOwner" in n && n.onlyOwner) || managesMembers(session.role))} />
          )}
        </div>
      </header>
      {getMode() === "sample" && <p className="border-b border-accent/30 bg-accent/10 px-4 py-2 text-center font-mono text-[12px] text-accent">{app.sample}</p>}
      <main id="contenido" className="mx-auto max-w-5xl px-4 pb-24 pt-10 sm:pt-14">{children}</main>
    </>
  );
}
