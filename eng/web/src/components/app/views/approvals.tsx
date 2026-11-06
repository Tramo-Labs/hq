"use client";

import { app } from "@/content/app";
import { can, listMatches, listPending } from "@/lib/api";
import { Notice, PageHead } from "@/components/app/ui";
import { Decision } from "@/components/app/interactive";
import { MatchCard, MovementCard } from "@/components/app/items";
import { useData, useSession } from "@/components/app/session";
import { Loaded } from "./loaded";

/**
 * The client's Exceptions inbox: matches an operator escalated, and movements the latest
 * run left with no candidate. Roles that cannot decide (viewer) see it without buttons.
 */
export function Approvals() {
  const a = app.approvals;
  const session = useSession();
  const canDecide = can(session.role, "decideException");
  const state = useData(() => Promise.all([listMatches("exception"), listPending("exception")]), session.tenantId);
  return (
    <Loaded state={state}>
      {([{ items: matches }, { items: movements }]) => {
        const n = matches.length + movements.length;
        return (
          <>
            <PageHead eyebrow={a.eyebrow} title={a.title} body={a.body}
              aside={<p className="font-mono text-[13px] text-accent">{a.pending(n)}</p>} />
            {!canDecide && <p className="mb-4 text-[14.5px] text-white/70">{a.readOnly}</p>}
            {n === 0 ? <Notice>{a.empty}</Notice> : (
              <ul className="grid gap-4">
                {movements.map((p) => (
                  <MovementCard key={p.movementId} p={p} title={a.movementTitle}>
                    {canDecide && <Decision target={{ movement: p.movementId }} variant="client" movement={p} />}
                  </MovementCard>
                ))}
                {matches.map((m) => (
                  <MatchCard key={m.id} m={m} title={a.matchTitle(app.rules[m.rule] ?? m.rule)}>
                    {canDecide && <Decision target={{ match: m.id }} variant="client" />}
                  </MatchCard>
                ))}
              </ul>
            )}
            <p className="mt-6 text-[13px] text-white/60">{a.final}</p>
          </>
        );
      }}
    </Loaded>
  );
}
