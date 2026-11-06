import type { Metadata } from "next";
import Link from "next/link";
import { app } from "@/content/app";
import { MatchCard, PageHead } from "@/components/app/ui";
import { Decision } from "@/components/app/interactive";
import { ghostBtn } from "@/components/app/buttons";
import { can, daysSince, getSession, listMatches, listTenants } from "@/lib/api";
import type { Motive, Tray } from "@/lib/api";

export const metadata: Metadata = { title: app.trays.eyebrow };

const trays: Tray[] = ["suggested", "exception", "auto"];
const one = (v: string | string[] | undefined) => (Array.isArray(v) ? v[0] : v);

export default async function Trays({ searchParams }: { searchParams: Promise<Record<string, string | string[] | undefined>> }) {
  const tr = app.trays;
  const sp = await searchParams;
  const tray = trays.find((t) => t === one(sp.bandeja)) ?? "suggested";
  const [session, { items: tenants }, { items: inTray }] = await Promise.all([getSession("console"), listTenants(), listMatches({ tray })]);
  const tenantId = tenants.find((t) => t.id === one(sp.cliente))?.id;
  const motives = [...new Set(inTray.map((m) => m.motive))];
  const motive = motives.find((m) => m === one(sp.motivo)) as Motive | undefined;
  const items = inTray.filter((m) => (!tenantId || m.tenantId === tenantId) && (!motive || m.motive === motive));
  const tenantName = (id: string) => tenants.find((t) => t.id === id)?.name ?? id;
  const counts = Object.fromEntries(await Promise.all(trays.map(async (t) => [t, (await listMatches({ tray: t })).items.length] as const)));

  return (
    <>
      <PageHead eyebrow={tr.eyebrow} title={tr.title} />

      <nav aria-label={tr.eyebrow} className="flex flex-wrap gap-2">
        {trays.map((t) => (
          <Link key={t} href={`/operador/bandejas?bandeja=${t}`} aria-current={t === tray ? "page" : undefined}
            className={`inline-flex min-h-11 items-center gap-2 rounded-full border px-4 text-[14px] ${t === tray ? "border-white bg-white text-ink" : "border-white/25 text-white/80 hover:bg-white/10"}`}>
            {tr.names[t]} <span className="font-mono text-[12px]">{counts[t]}</span>
          </Link>
        ))}
      </nav>
      <p className="mt-4 max-w-2xl text-[15px] leading-relaxed text-white/70">{tr.hints[tray]}</p>

      <form method="get" className="mt-6 grid gap-3 sm:flex sm:flex-wrap sm:items-end">
        <input type="hidden" name="bandeja" value={tray} />
        <label className="grid gap-1 text-[13px] text-white/70">
          {tr.filterClient}
          <select name="cliente" defaultValue={tenantId ?? ""} className="min-h-11 rounded-lg border border-white/20 bg-ink-2 px-3 text-[15px] text-white">
            <option value="">{tr.all}</option>
            {tenants.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
          </select>
        </label>
        <label className="grid gap-1 text-[13px] text-white/70">
          {tr.filterMotive}
          <select name="motivo" defaultValue={motive ?? ""} className="min-h-11 rounded-lg border border-white/20 bg-ink-2 px-3 text-[15px] text-white">
            <option value="">{tr.all}</option>
            {motives.map((m) => <option key={m} value={m}>{app.motives[m]}</option>)}
          </select>
        </label>
        <button type="submit" className={`${ghostBtn} w-fit`}>{tr.apply}</button>
      </form>

      {items.length === 0 ? (
        <p className="mt-6 rounded-xl border border-white/10 p-6 text-[15px] text-white/70">{tr.empty}</p>
      ) : (
        <ul className="mt-6 grid gap-4">
          {items.map((m) => (
            <MatchCard key={m.id} m={m} meta={`${tenantName(m.tenantId)} · ${app.motives[m.motive]} · ${tr.age(daysSince(m.openedAt))}`}>
              {tray === "suggested" && can(session.role, "decideSuggested") && <Decision matchId={m.id} variant="operator" by={session.name} hasProposal={!!m.proposal} />}
              {tray === "exception" && <p className="text-[14px] text-accent">{tr.waitingClient}</p>}
            </MatchCard>
          ))}
        </ul>
      )}
    </>
  );
}
