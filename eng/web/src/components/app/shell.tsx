"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useState, type ReactNode } from "react";
import { site } from "@/content/site";
import { app } from "@/content/app";
import { areaAllows, getMode, homeFor, type Session } from "@/lib/api";
import { SessionGate, useSignOut } from "@/components/app/session";
import { Notice } from "@/components/app/ui";

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
  function out() {
    setLeaving(true);
    signOut();
  }
  return (
    <div className="flex w-full flex-wrap items-center gap-x-3 gap-y-1 text-[13px] text-white/70 sm:w-auto">
      <span className="min-w-0 break-all">{s.signedAs(session.email, app.roles[session.role])}</span>
      <span className="text-white">{session.tenantName}</span>
      {session.memberships.length > 1 && (
        <Link href={`/entrar?cambiar=1&volver=${encodeURIComponent(path)}`} className="inline-flex min-h-11 items-center underline decoration-white/40 underline-offset-4 hover:text-white">
          {s.switchTitle}
        </Link>
      )}
      <button type="button" onClick={out} disabled={leaving} aria-busy={leaving}
        className="inline-flex min-h-11 items-center rounded-full border border-white/25 px-3 text-paper hover:bg-white/10 disabled:opacity-60">
        {leaving ? s.loggingOut : s.logout}
      </button>
    </div>
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
            <Link href="/" className="inline-flex min-h-11 items-center text-[17px] font-medium tracking-tight">{site.name}</Link>
            <span className="font-mono text-[12px] text-white/60">{copy.label}</span>
          </p>
          {session && <Account session={session} />}
          {session && areaAllows(area, session.role) && <NavLinks items={copy.nav} />}
        </div>
      </header>
      {getMode() === "sample" && <p className="border-b border-accent/30 bg-accent/10 px-4 py-2 text-center font-mono text-[12px] text-accent">{app.sample}</p>}
      <main id="contenido" className="mx-auto max-w-5xl px-4 pb-24 pt-10 sm:pt-14">{children}</main>
    </>
  );
}
