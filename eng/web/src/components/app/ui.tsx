import type { ReactNode } from "react";
import { app } from "@/content/app";
import type { StepStatus } from "@/lib/api";

const money = new Intl.NumberFormat("es-AR", { style: "currency", currency: "ARS", maximumFractionDigits: 0 });

export const formatMoney = (cents: number) => money.format(cents / 100);
/** "2026-11-03" or a full timestamp -> "03/11", in Argentina time for timestamps. */
export const formatDate = (iso: string) => {
  const d = iso.length > 10 ? new Date(Date.parse(iso) - 3 * 3_600_000).toISOString() : iso;
  return `${d.slice(8, 10)}/${d.slice(5, 7)}`;
};

/** Loading, error and empty states shared by every screen. */
export function Notice({ children, tone = "plain" }: { children: ReactNode; tone?: "plain" | "alert" }) {
  return (
    <div role={tone === "alert" ? "alert" : "status"} className={`rounded-xl border p-6 text-[15px] ${tone === "alert" ? "border-accent/40 text-accent" : "border-white/10 text-white/70"}`}>
      {children}
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
  verified: "bg-white text-ink",
  pending: "border border-dashed border-white/40 text-white/75",
  failed: "border border-accent bg-accent/10 text-accent",
};

export function StatusPill({ status }: { status: StepStatus }) {
  return <span className={`inline-block whitespace-nowrap rounded px-2 py-0.5 font-mono text-[11.5px] ${pill[status]}`}>{app.status[status]}</span>;
}

export function Card({ children, className = "" }: { children: ReactNode; className?: string }) {
  return <div className={`rounded-xl border border-white/10 bg-ink-2 ${className}`}>{children}</div>;
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
