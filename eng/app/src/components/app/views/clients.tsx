"use client";

import Link from "next/link";
import { app } from "@/content/app";
import { listTenants, missingSteps } from "@/lib/api";
import { PageHead, StatusPill } from "@/components/app/ui";
import { ghostBtn } from "@/components/app/buttons";
import { useData, useSession } from "@/components/app/session";
import { Loaded } from "./loaded";

/**
 * The operator's clients, most stuck first. Trays and numbers are per tenant (the tenant
 * comes from the token), so working on another client means logging in again for it.
 */
export function Clients() {
  const c = app.clients;
  const session = useSession();
  const state = useData(listTenants, session.email);
  return (
    <>
      <PageHead eyebrow={c.eyebrow} title={c.title} body={c.body} />
      <Loaded state={state}>
        {({ items }) => (
          <ul className="grid gap-4">
            {[...items].sort((a, b) => missingSteps(b) - missingSteps(a) || b.openExceptions - a.openExceptions).map((t) => {
              const missing = missingSteps(t);
              const active = t.id === session.tenantId;
              return (
                <li key={t.id} className={`rounded-xl border bg-ink-2 p-5 sm:p-6 ${active ? "border-white/40" : "border-white/10"}`}>
                  <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
                    <h2 className="text-[18px] text-white">{t.name}</h2>
                    {t.cuit && <span className="font-mono text-[12.5px] text-white/60">{app.onboarding.cuit(t.cuit)}</span>}
                  </div>
                  <p className="mt-1 text-[14px] text-white/70">
                    <span className={missing > 0 ? "text-accent" : "text-white"}>{c.missing(missing)}</span>
                    {" · "}{c.exceptions(t.openExceptions, t.openExceptionsCapped)}
                    {" · "}{app.roles[t.role]}
                  </p>
                  <dl className="mt-4 grid gap-3 sm:grid-cols-3">
                    {t.steps.map((s) => (
                      <div key={s.kind} className="flex items-center justify-between gap-2 rounded-lg border border-white/10 px-3 py-2">
                        <dt className="text-[14px] text-white/85">{c.steps[s.kind]}</dt>
                        <dd><StatusPill status={s.status} /></dd>
                      </div>
                    ))}
                  </dl>
                  <div className="mt-4">
                    {active ? <p className="font-mono text-[12.5px] text-white/70">{c.active}</p> : (
                      <Link href={`/entrar?cambiar=1&empresa=${t.id}&volver=${encodeURIComponent("/operador/bandejas")}`} className={ghostBtn}>{c.work}</Link>
                    )}
                  </div>
                </li>
              );
            })}
          </ul>
        )}
      </Loaded>
    </>
  );
}
