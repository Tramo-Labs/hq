"use client";

import { app } from "@/content/app";
import { getMyTenant, listAccounts, missingSteps } from "@/lib/api";
import type { OnboardingStep } from "@/lib/api";
import { PageHead, StatusPill, formatDate } from "@/components/app/ui";
import { ghostBtn } from "@/components/app/buttons";
import { useData, useSession } from "@/components/app/session";
import { MercadoPagoBlock } from "@/components/app/mercadopago";
import { Loaded } from "./loaded";

const o = app.onboarding;

/** The core explains a step in English; known sentences are translated, the rest falls back to the status. */
function detail(step: OnboardingStep) {
  const statement = /^statement accepted for (.+)$/.exec(step.detail);
  if (statement) return o.statementFor(statement[1]);
  return o.details[step.detail] ?? app.status[step.status];
}

export function PortalHome() {
  const session = useSession();
  const state = useData(() => Promise.all([getMyTenant(), listAccounts()]), session.tenantId);
  return (
    <Loaded state={state}>
      {([t, { items: accounts }]) => (
        <>
          <PageHead eyebrow={`${o.eyebrow} · ${t.name}`} title={o.title} body={o.body}
            aside={<p className="font-mono text-[13px] text-white/70">{o.progress(3 - missingSteps(t))}</p>} />
          <ol className="grid gap-4">
            {t.steps.map((step, i) => (
              <li key={step.kind} className="rounded-xl border border-white/10 bg-ink-2 p-5 sm:p-6">
                <div className="flex items-start justify-between gap-3">
                  <h2 className="text-[20px] font-light"><span className="mr-2 font-mono text-[13px] text-accent">0{i + 1}</span>{o.names[step.kind]}</h2>
                  <StatusPill status={step.status} />
                </div>
                <p className="mt-3 text-[15px] leading-relaxed text-white/70">{o.bodies[step.kind]}</p>
                <p className="mt-3 text-[14.5px] text-white">
                  {detail(step)}
                  {step.verifiedAt && <span className="text-white/60"> · {o.verifiedAt(formatDate(step.verifiedAt))}</span>}
                </p>
                {step.kind === "arca" && t.cuit && <p className="mt-1 font-mono text-[13px] text-white/70">{o.cuit(t.cuit)}</p>}
                {step.kind === "bank" && (
                  <>
                    <p className="mt-3 font-mono text-[12px] text-white/60">{o.accounts(accounts.length)}</p>
                    {accounts.length > 0 && (
                      <ul className="mt-2 divide-y divide-white/10 border-y border-white/10">
                        {accounts.map((a) => (
                          <li key={a.id} className="py-3 text-[14.5px] text-white">
                            {a.name} · {o.banks[a.bank]} · {a.currency}{a.number && <span className="font-mono text-[13px] text-white/60"> · ****{a.number.slice(-4)}</span>}
                          </li>
                        ))}
                      </ul>
                    )}
                    <MercadoPagoBlock />
                  </>
                )}
                {step.status !== "verified" && (
                  <div className="mt-5 flex flex-wrap items-center gap-2">
                    {o.actions[step.kind].map((label) => <button key={label} type="button" disabled className={ghostBtn} aria-describedby={`soon-${step.kind}`}>{label}</button>)}
                    <span id={`soon-${step.kind}`} className="text-[13px] text-white/60">{o.soon}</span>
                  </div>
                )}
              </li>
            ))}
          </ol>
        </>
      )}
    </Loaded>
  );
}
