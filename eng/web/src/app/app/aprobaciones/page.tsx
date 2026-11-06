import type { Metadata } from "next";
import { app } from "@/content/app";
import { MatchCard, PageHead } from "@/components/app/ui";
import { Decision } from "@/components/app/interactive";
import { can, getCurrentTenant, getSession, listMatches } from "@/lib/api";

export const metadata: Metadata = { title: app.approvals.eyebrow };

export default async function Approvals() {
  const a = app.approvals;
  const [tenant, session] = await Promise.all([getCurrentTenant(), getSession("portal")]);
  const { items } = await listMatches({ tray: "exception", tenantId: tenant.id });
  const canDecide = can(session.role, "decideException");
  return (
    <>
      <PageHead eyebrow={a.eyebrow} title={a.title} body={a.body}
        aside={<p className="font-mono text-[13px] text-accent">{a.pending(items.length)}</p>} />
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
