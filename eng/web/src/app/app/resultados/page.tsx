import type { Metadata } from "next";
import { app } from "@/content/app";
import { PageHead } from "@/components/app/ui";
import { ResultsPanel } from "@/components/app/results";
import { getCurrentTenant, listMetrics } from "@/lib/api";

export const metadata: Metadata = { title: app.results.eyebrow };

/** The client's panel (spec section 4): only numbers whose list the client can open. */
export default async function Results() {
  const r = app.results;
  const tenant = await getCurrentTenant();
  const { items } = await listMetrics(tenant.id);
  return (
    <>
      <PageHead eyebrow={`${r.eyebrow} · ${tenant.name}`} title={r.title} body={r.body} />
      <ResultsPanel metrics={items} area="portal" tenantId={tenant.id} />
      <p className="mt-6 text-[13px] text-white/60">{r.hoursNotePortal}</p>
    </>
  );
}
