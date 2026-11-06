import type { ReactNode } from "react";
import Link from "next/link";
import { site } from "@/content/site";
import { app } from "@/content/app";
import type { Match, Session, StepStatus } from "@/lib/api";
import { isSample } from "@/lib/api";
import { NavLinks } from "@/components/app/interactive";

const money = new Intl.NumberFormat("es-AR", { style: "currency", currency: "ARS", maximumFractionDigits: 0 });

export const formatMoney = (cents: number) => money.format(cents / 100);
/** "2026-11-03" -> "03/11". Parsed by hand so the server timezone never shifts the day. */
export const formatDate = (iso: string) => `${iso.slice(8, 10)}/${iso.slice(5, 7)}`;

/** Header, nav and the sample-data notice shared by the portal and the console. */
export function Shell({ area, session, children }: { area: "portal" | "console"; session: Session; children: ReactNode }) {
  const copy = area === "portal" ? app.portal : app.console;
  return (
    <div className="min-h-dvh">
      <a href="#contenido" className="sr-only rounded bg-paper px-4 text-ink focus:not-sr-only focus:fixed focus:left-4 focus:top-4 focus:z-50 focus:inline-flex focus:min-h-11 focus:items-center">{app.skip}</a>
      <header className="sticky top-0 z-20 border-b border-white/10 bg-ink/90 backdrop-blur">
        <div className="mx-auto flex max-w-5xl flex-wrap items-center justify-between gap-x-6 gap-y-3 px-4 py-3">
          <p className="flex items-center gap-2">
            <Link href="/" className="inline-flex min-h-11 items-center text-[17px] font-medium tracking-tight">{site.name}</Link>
            <span className="font-mono text-[12px] text-white/60">{copy.label}</span>
          </p>
          <p className="text-[13px] text-white/60">{app.signedAs(session.name, app.roles[session.role])}</p>
          <NavLinks items={copy.nav} />
        </div>
      </header>
      {isSample && <p className="border-b border-accent/30 bg-accent/10 px-4 py-2 text-center font-mono text-[12px] text-accent">{app.sample}</p>}
      <main id="contenido" className="mx-auto max-w-5xl px-4 pb-24 pt-10 sm:pt-14">{children}</main>
    </div>
  );
}

export function PageHead({ eyebrow, title, body, aside }: { eyebrow: string; title: string; body?: string; aside?: ReactNode }) {
  return (
    <div className="mb-8 flex flex-wrap items-end justify-between gap-4">
      <div className="max-w-2xl">
        <p className="font-mono text-[12px] tracking-wide text-white/60">{eyebrow}</p>
        <h1 className="mt-3 text-[30px] font-light leading-[1.1] tracking-[-0.01em] sm:text-[40px]">{title}</h1>
        {body && <p className="mt-4 text-[16px] leading-relaxed text-white/70">{body}</p>}
      </div>
      {aside}
    </div>
  );
}

const pill: Record<StepStatus, string> = {
  ready: "bg-white text-ink",
  checking: "border border-white/30 text-white/80",
  pending: "border border-dashed border-white/40 text-white/75",
  problem: "border border-accent bg-accent/10 text-accent",
};

export function StatusPill({ status }: { status: StepStatus }) {
  return <span className={`inline-block whitespace-nowrap rounded px-2 py-0.5 font-mono text-[11.5px] ${pill[status]}`}>{app.status[status]}</span>;
}

export function Card({ children, className = "" }: { children: ReactNode; className?: string }) {
  return <div className={`rounded-xl border border-white/10 bg-ink-2 ${className}`}>{children}</div>;
}

/** One item of a tray: what it is, the evidence, why it is here, the proposal and the rule. */
export function MatchCard({ m, meta, children }: { m: Match; meta?: ReactNode; children?: ReactNode }) {
  const a = app.approvals;
  return (
    <li className="rounded-xl border border-white/10 bg-ink-2 p-5 sm:p-6">
      {meta && <p className="mb-2 font-mono text-[12px] text-white/60">{meta}</p>}
      <h2 className="text-[18px] leading-snug text-white">{m.summary}</h2>
      <dl className="mt-4 grid gap-4 sm:grid-cols-2">
        <Field label={a.movement}>
          {formatDate(m.movement.date)} · {m.movement.account}<br />
          <span className="font-mono text-[13px] text-white/70">{m.movement.description}</span><br />
          <span className="tabular-nums">{formatMoney(m.movement.amountCents)}</span>
        </Field>
        <Field label={a.document}>
          {m.document ? (
            <>{m.document.kind} {m.document.number}<br />{m.document.counterparty}<br /><span className="tabular-nums">{formatMoney(m.document.amountCents)}</span></>
          ) : <span className="text-white/70">{a.noDocument}</span>}
        </Field>
        <Field label={a.why}>{m.explanation}</Field>
        <Field label={a.proposal}>{m.proposal ?? <span className="text-white/70">{a.noProposal}</span>}</Field>
      </dl>
      <p className="mt-4 font-mono text-[12px] text-white/60">{a.rule(m.rule.name, m.rule.version)}</p>
      {children && <div className="mt-5 border-t border-white/10 pt-4">{children}</div>}
    </li>
  );
}

/** Label and value pair used in cards; stacks on narrow screens. */
export function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="min-w-0">
      <dt className="font-mono text-[11px] uppercase tracking-widest text-white/60">{label}</dt>
      <dd className="mt-1 break-words text-[14.5px] text-white/85">{children}</dd>
    </div>
  );
}
