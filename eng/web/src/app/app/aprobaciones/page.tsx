import type { Metadata } from "next";
import Link from "next/link";
import { app } from "@/content/app";
import { MatchCard, PageHead } from "@/components/app/ui";
import { Decision } from "@/components/app/interactive";
import { can, getCurrentTenant, getSession, listMatches } from "@/lib/api";

export const metadata: Metadata = { title: app.approvals.eyebrow };

const one = (v: string | string[] | undefined) => (Array.isArray(v) ? v[0] : v);

export default async function Approvals({ searchParams }: { searchParams: Promise<Record<string, string | string[] | undefined>> }) {
  const a = app.approvals;
  const sp = await searchParams;
  const [tenant, session] = await Promise.all([getCurrentTenant(), getSession("portal")]);
  const { items: all } = await listMatches({ tray: "exception", tenantId: tenant.id });
  // ?motivo= comes from a panel number, so the list matches what the number counts.
  const motive = all.find((m) => m.motive === one(sp.motivo))?.motive;
  const items = motive ? all.filter((m) => m.motive === motive) : all;
  const canDecide = can(session.role, "decideException");
  return (
    <>
      <PageHead eyebrow={a.eyebrow} title={a.title} body={a.body}
        aside={<p className="font-mono text-[13px] text-accent">{a.pending(items.length)}</p>} />
      {motive && (
        <p className="mb-4 flex flex-wrap items-center gap-x-3 text-[14.5px] text-white/70">
          <span>{app.trays.filterMotive}: <span className="text-white">{app.motives[motive]}</span></span>
          <Link href="/app/aprobaciones" className="inline-flex min-h-11 items-center text-white underline decoration-white/40 underline-offset-4">{app.trays.all}</Link>
        </p>
      )}
      {!canDecide && <p className="mb-4 text-[14.5px] text-white/70">{a.readOnly}</p>}
      {items.length === 0 ? (
        <p className="rounded-xl border border-white/10 p-6 text-[15px] text-white/70">{a.empty}</p>
      ) : (
        <ul className="grid gap-4">
          {items.map((m) => (
            <MatchCard key={m.id} m={m} meta={app.motives[m.motive]}>
              {canDecide && <Decision matchId={m.id} variant="client" by={session.name} hasProposal={!!m.proposal} />}
            </MatchCard>
          ))}
        </ul>
      )}
      <p className="mt-6 text-[13px] text-white/60">{a.final}</p>
    </>
  );
}
