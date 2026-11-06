import { app } from "@/content/app";
import { PageHead, StatusPill, formatDate } from "@/components/app/ui";
import { daysSince, listTenants, missingSteps } from "@/lib/api";


export default async function Clients() {
  const c = app.clients;
  const { items } = await listTenants();
  return (
    <>
      <PageHead eyebrow={c.eyebrow} title={c.title} body={c.body} />
      <ul className="grid gap-4">
        {items.map((t) => {
          const missing = missingSteps(t);
          return (
            <li key={t.id} className="rounded-xl border border-white/10 bg-ink-2 p-5 sm:p-6">
              <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
                <h2 className="text-[18px] text-white">{t.name}</h2>
                <span className="font-mono text-[12.5px] text-white/60">{t.cuit}</span>
              </div>
              <p className="mt-1 text-[14px] text-white/70">
                <span className={missing > 0 ? "text-accent" : "text-white"}>{c.missing(missing)}</span>
                {missing > 0 && t.waitingSince && <> · {c.waiting(daysSince(t.waitingSince))}</>}
                {" · "}{c.exceptions(t.openExceptions)}
              </p>
              <dl className="mt-4 grid gap-3 sm:grid-cols-3">
                {([["arca", t.arca.status], ["bank", t.bank.status], ["erp", t.erp.status]] as const).map(([k, s]) => (
                  <div key={k} className="flex items-center justify-between gap-2 rounded-lg border border-white/10 px-3 py-2">
                    <dt className="text-[14px] text-white/85">{c.steps[k]}</dt>
                    <dd><StatusPill status={s} /></dd>
                  </div>
                ))}
              </dl>
              {t.bank.accounts.length > 0 && (
                <ul className="mt-3 grid gap-1 text-[13px] text-white/70">
                  {t.bank.accounts.map((a) => (
                    <li key={a.last4}>
                      {a.bank} ****{a.last4} · {a.lastStatementAt ? app.onboarding.bank.lastStatement(formatDate(a.lastStatementAt)) : app.onboarding.bank.noStatement}
                      {a.problem && <span className="block text-accent">{a.problem}</span>}
                    </li>
                  ))}
                </ul>
              )}
            </li>
          );
        })}
      </ul>
    </>
  );
}
