import type { Metadata } from "next";
import { app } from "@/content/app";
import { PageHead } from "@/components/app/ui";
import { ResultsPanel } from "@/components/app/results";
import { ghostBtn } from "@/components/app/buttons";
import { listMetrics, listTenants } from "@/lib/api";

export const metadata: Metadata = { title: app.results.eyebrow };

const one = (v: string | string[] | undefined) => (Array.isArray(v) ? v[0] : v);

/** The same panel the client sees, for one client at a time (spec section 6). */
export default async function Results({ searchParams }: { searchParams: Promise<Record<string, string | string[] | undefined>> }) {
  const r = app.results;
  const sp = await searchParams;
  const { items: tenants } = await listTenants();
  const panels = await Promise.all(tenants.map(async (t) => [t.id, (await listMetrics(t.id)).items] as const));
  const tenantId = tenants.find((t) => t.id === one(sp.cliente))?.id ?? panels.find(([, m]) => m.length > 0)?.[0] ?? tenants[0].id;
  const items = panels.find(([id]) => id === tenantId)?.[1] ?? [];
  return (
    <>
      <PageHead eyebrow={r.eyebrow} title={r.title} body={r.body} />
      <form method="get" className="mb-6 flex flex-wrap items-end gap-3">
        <label className="grid gap-1 text-[13px] text-white/70">
          {r.client}
          <select name="cliente" defaultValue={tenantId} className="min-h-11 max-w-full rounded-lg border border-white/20 bg-ink-2 px-3 text-[15px] text-white">
            {tenants.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
          </select>
        </label>
        <button type="submit" className={ghostBtn}>{app.trays.apply}</button>
      </form>
      <ResultsPanel metrics={items} area="console" tenantId={tenantId} />
      <p className="mt-6 text-[13px] text-white/60">{r.hoursNote}</p>
    </>
  );
}
