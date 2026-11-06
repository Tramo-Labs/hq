import type { ReactNode } from "react";
import { app } from "@/content/app";
import type { Match, PendingMovement } from "@/lib/api";
import { Field, formatMoney } from "@/components/app/ui";

const a = app.approvals;
const rule = (id: string) => app.rules[id] ?? id;

function Shell({ meta, title, children, actions }: { meta?: ReactNode; title: string; children: ReactNode; actions?: ReactNode }) {
  return (
    <li className="rounded-xl border border-white/10 bg-ink-2 p-5 sm:p-6">
      {meta && <p className="mb-2 font-mono text-[12px] text-white/60">{meta}</p>}
      <h2 className="text-[18px] leading-snug text-white">{title}</h2>
      {children}
      {actions && <div className="mt-5 border-t border-white/10 pt-4">{actions}</div>}
    </li>
  );
}

/**
 * A match as the core returns it: rule, evidence ids, reference, adjustments and flags.
 * The core does not send movement or invoice details yet (date, amount, counterparty), so
 * the card shows what the match itself says and nothing invented.
 */
export function MatchCard({ m, meta, title, children }: { m: Match; meta?: ReactNode; title?: string; children?: ReactNode }) {
  const notes = [m.dateDiffDays !== undefined && a.days(m.dateDiffDays), m.crossPeriod && a.crossPeriod, m.lowConfidence && a.lowConfidence].filter(Boolean) as string[];
  return (
    <Shell meta={meta} title={title ?? rule(m.rule)} actions={children}>
      <dl className="mt-4 grid gap-4 sm:grid-cols-2">
        <Field label={a.evidence}>{a.movements(m.movementIds.length)} · {a.items(m.itemIds.length)}</Field>
        {m.reference && <Field label={a.reference}><span className="font-mono text-[13.5px]">{m.reference}</span></Field>}
        {notes.length > 0 && <Field label={a.why}>{notes.join(" ")}</Field>}
        {m.adjustments.length > 0 && (
          <Field label={a.adjustments}>
            {m.adjustments.map((x, i) => (
              <span key={i} className="block"><span className="tabular-nums">{formatMoney(x.amount)}</span> · {a.adjustmentSource[x.source]}</span>
            ))}
          </Field>
        )}
      </dl>
      <p className="mt-4 font-mono text-[12px] text-white/60">{a.rule(rule(m.rule), m.ruleVersion)}</p>
    </Shell>
  );
}

/** A movement the latest run left without a match, with its reason and candidates. */
export function MovementCard({ p, meta, title, children }: { p: PendingMovement; meta?: ReactNode; title: string; children?: ReactNode }) {
  return (
    <Shell meta={meta} title={title} actions={children}>
      <dl className="mt-4 grid gap-4 sm:grid-cols-2">
        <Field label={a.why}>{app.reasons[p.reason]}</Field>
        <Field label={a.evidence}>
          {app.trays.candidates(p.candidates.length)}
          {p.candidates.map((c, i) => <span key={i} className="block text-white/70">{rule(c.rule)} · {a.items(c.itemIds.length)}</span>)}
        </Field>
        {p.note && <Field label={app.trays.clientNote}>{p.note}</Field>}
      </dl>
      {p.reason === "no_candidate" && <p className="mt-4 text-[14px] text-white/70">{a.noProposal}</p>}
    </Shell>
  );
}
