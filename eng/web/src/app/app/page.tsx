import type { ReactNode } from "react";
import { app } from "@/content/app";
import { PageHead, StatusPill, formatDate } from "@/components/app/ui";
import { ghostBtn } from "@/components/app/buttons";
import { getCurrentTenant, missingSteps } from "@/lib/api";
import type { StepStatus } from "@/lib/api";


const o = app.onboarding;

function Step({ n, name, status, body, children, actions }: { n: number; name: string; status: StepStatus; body: string; children?: ReactNode; actions: string[] }) {
  return (
    <li className="rounded-xl border border-white/10 bg-ink-2 p-5 sm:p-6">
        <div className="flex items-start justify-between gap-3">
          <h2 className="text-[20px] font-light"><span className="mr-2 font-mono text-[13px] text-accent">0{n}</span>{name}</h2>
          <StatusPill status={status} />
        </div>
        <p className="mt-3 text-[15px] leading-relaxed text-white/70">{body}</p>
        {children && <div className="mt-4">{children}</div>}
        {status !== "ready" && (
          <div className="mt-5 flex flex-wrap items-center gap-2">
            {actions.map((a) => <button key={a} type="button" disabled className={ghostBtn} aria-describedby={`soon-${n}`}>{a}</button>)}
            <span id={`soon-${n}`} className="text-[13px] text-white/60">{o.soon}</span>
          </div>
        )}
    </li>
  );
}

export default async function Onboarding() {
  const t = await getCurrentTenant();
  const done = 3 - missingSteps(t);
  return (
    <>
      <PageHead eyebrow={`${o.eyebrow} · ${t.name}`} title={o.title} body={o.body}
        aside={<p className="font-mono text-[13px] text-white/70" aria-live="polite">{o.progress(done)}</p>} />
      <ol className="grid gap-4">
        <Step n={1} name={o.arca.name} status={t.arca.status} body={o.arca.body} actions={[o.arca.action]}>
          {t.arca.status === "ready" && t.arca.confirmedAt && <p className="text-[14.5px] text-white">{o.arca.ready(t.cuit, formatDate(t.arca.confirmedAt))}</p>}
          {t.arca.problem && <p className="text-[14.5px] text-accent">{t.arca.problem}</p>}
        </Step>

        <Step n={2} name={o.bank.name} status={t.bank.status} body={o.bank.body} actions={[o.bank.upload, o.bank.mercadoPago]}>
          <p className="font-mono text-[12px] text-white/60">{o.bank.accounts(t.bank.accounts.length)}</p>
          {t.bank.accounts.length > 0 && (
            <ul className="mt-2 divide-y divide-white/10 border-y border-white/10">
              {t.bank.accounts.map((a) => (
                <li key={a.last4} className="py-3">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <span className="text-[14.5px] text-white">{a.bank} · {a.kind} · {a.currency} · ****{a.last4}</span>
                    <StatusPill status={a.status} />
                  </div>
                  <p className="mt-1 text-[13px] text-white/60">{a.lastStatementAt ? o.bank.lastStatement(formatDate(a.lastStatementAt)) : o.bank.noStatement}</p>
                  {a.problem && <p className="mt-2 text-[14px] leading-relaxed text-accent">{a.problem}</p>}
                </li>
              ))}
            </ul>
          )}
          <p className="mt-4 text-[13px] text-white/60">{o.bank.mailbox}</p>
          <p className="mt-1 break-all font-mono text-[14px] text-white">{t.bank.mailbox}</p>
          {t.bank.status === "ready" && (
            <div className="mt-4 flex flex-wrap gap-2">
              {[o.bank.addAccount, o.bank.upload].map((a) => <button key={a} type="button" disabled className={ghostBtn} title={o.soon}>{a}</button>)}
            </div>
          )}
        </Step>

        <Step n={3} name={o.erp.name} status={t.erp.status} body={o.erp.body} actions={[o.erp.action]}>
          {t.erp.system && (
            <div className="flex flex-wrap items-center justify-between gap-2">
              <span className="font-mono text-[14px] text-white">{o.erp.connected(t.erp.system, t.erp.credentialTail)}</span>
              {t.erp.mode === "api" && <button type="button" disabled className={ghostBtn} title={o.soon}>{o.erp.revoke}</button>}
            </div>
          )}
          {t.erp.problem && <p className="mt-2 text-[14.5px] text-accent">{t.erp.problem}</p>}
        </Step>
      </ol>
    </>
  );
}
