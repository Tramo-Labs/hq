"use client";

import Link from "next/link";
import { app } from "@/content/app";
import { can, listMatches, listPending } from "@/lib/api";
import type { MatchTray } from "@/lib/api";
import { Notice, PageHead } from "@/components/app/ui";
import { Decision } from "@/components/app/interactive";
import { MatchCard, MovementCard } from "@/components/app/items";
import { useData, useSession } from "@/components/app/session";
import { Loaded } from "./loaded";

/**
 * The trays of the active tenant. Suggested is the operator's alone (the core answers 403 to
 * anyone else, accountant included), so an accountant sees Exceptions and Automático and
 * opens on Exceptions. Exceptions waits for the client; Automático is decided by nobody.
 */
export function Trays({ asked }: { asked?: MatchTray }) {
  const tr = app.trays;
  const session = useSession();
  const trayOrder: MatchTray[] = session.role === "operator" ? ["suggested", "exception", "auto"] : ["exception", "auto"];
  const tray = asked && trayOrder.includes(asked) ? asked : trayOrder[0];
  const counts = useData(
    () => Promise.all(trayOrder.map(async (t) => {
      const [m, p] = await Promise.all([listMatches(t), t === "auto" ? null : listPending(t)]);
      return m.items.length + (p?.items.length ?? 0);
    })),
    `${session.tenantId}:counts`,
  );
  const state = useData(() => Promise.all([listMatches(tray), tray === "auto" ? Promise.resolve(null) : listPending(tray)]), `${session.tenantId}:${tray}`);
  const decides = tray === "suggested" && can(session.role, "decideSuggested");

  return (
    <>
      <PageHead eyebrow={`${tr.eyebrow} · ${session.tenantName}`} title={tr.title} />
      <nav aria-label={tr.eyebrow} className="flex flex-wrap gap-2">
        {trayOrder.map((t, i) => (
          <Link key={t} href={`/operador/bandejas?bandeja=${t}`} aria-current={t === tray ? "page" : undefined}
            className={`inline-flex min-h-11 items-center gap-2 rounded-full border px-4 text-[14px] ${t === tray ? "border-white bg-white text-ink" : "border-white/25 text-white/80 hover:bg-white/10"}`}>
            {tr.names[t]} {counts.status === "ready" && <span className="font-mono text-[12px]">{counts.data[i]}</span>}
          </Link>
        ))}
      </nav>
      <p className="mt-4 max-w-2xl text-[15px] leading-relaxed text-white/70">{tr.hints[tray]}</p>

      <div className="mt-6">
        <Loaded state={state}>
          {([{ items: matches }, pending]) => {
            const movements = pending?.items ?? [];
            if (matches.length + movements.length === 0) return <Notice>{tr.empty}</Notice>;
            return (
              <div className="grid gap-8">
                {matches.length > 0 && (
                  <section>
                    <h2 className="mb-3 font-mono text-[12px] tracking-wide text-white/60">{tr.matchesTitle}</h2>
                    <ul className="grid gap-4">
                      {matches.map((m) => (
                        <MatchCard key={m.id} m={m} meta={tr.of(session.tenantName)}>
                          {decides && <Decision target={{ match: m.id }} variant="operator" />}
                          {tray === "exception" && <p className="text-[14px] text-accent">{tr.waitingClient}</p>}
                        </MatchCard>
                      ))}
                    </ul>
                  </section>
                )}
                {movements.length > 0 && (
                  <section>
                    <h2 className="mb-3 font-mono text-[12px] tracking-wide text-white/60">{tr.movementsTitle}</h2>
                    <ul className="grid gap-4">
                      {movements.map((p) => (
                        <MovementCard key={p.movementId} p={p} meta={tr.of(session.tenantName)} title={app.reasons[p.reason]}>
                          {p.reason === "client_other" && can(session.role, "decideSuggested") ? (
                            <Decision target={{ movement: p.movementId }} variant="operator" />
                          ) : (
                            <p className={`text-[14px] ${tray === "exception" ? "text-accent" : "text-white/70"}`}>{tray === "exception" ? tr.waitingClient : tr.noMovementAction}</p>
                          )}
                        </MovementCard>
                      ))}
                    </ul>
                  </section>
                )}
              </div>
            );
          }}
        </Loaded>
      </div>
    </>
  );
}
