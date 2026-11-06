"use client";

import Link from "next/link";
import { app } from "@/content/app";
import { ApiError, currentPeriod, getMode, listMetrics, metricHref, periodPattern } from "@/lib/api";
import type { Metric } from "@/lib/api";
import { Notice, PageHead, formatMoney } from "@/components/app/ui";
import { ghostBtn } from "@/components/app/buttons";
import { useData, useSession } from "@/components/app/session";
import { metricTag } from "@/components/app/results-rules";
import { Loaded } from "./loaded";

const r = app.results;
type Area = "portal" | "console";

function show(m: Metric) {
  if (m.status === "no_data" || m.value === null) return r.noData;
  const v = m.value;
  if (m.unit === "percent") return `${v.toLocaleString("es-AR", { maximumFractionDigits: 1 })} %`;
  if (m.unit === "days") return r.days(v);
  if (m.unit === "hours") return r.hours(v);
  if (m.unit === "cents") return formatMoney(v);
  return v.toLocaleString("es-AR");
}

/**
 * The results panel of the active tenant, shared by the portal and the console. A number
 * whose list cannot be opened from this area is left out (spec section 4); "no data yet"
 * still shows, because it is not a number.
 */
export function Results({ area, period: asked }: { area: Area; period?: string }) {
  const session = useSession();
  const period = asked && periodPattern.test(asked) ? asked : currentPeriod();
  const state = useData(() => listMetrics(period).catch((e) => {
    if (e instanceof ApiError && e.status === 501) return null;
    throw e;
  }), `${session.tenantId}:${period}`);
  const mode = getMode();
  const base = area === "portal" ? "/app/resultados" : "/operador/resultados";
  return (
    <>
      <PageHead eyebrow={`${r.eyebrow} · ${session.tenantName}`} title={r.title} body={r.body} />
      <form method="get" action={base} className="mb-6 flex flex-wrap items-end gap-3">
        <label className="grid gap-1 text-[13px] text-white/70">
          {r.period}
          <input type="month" name="periodo" defaultValue={period} required
            className="min-h-11 rounded-lg border border-white/20 bg-ink-2 px-3 text-[15px] text-white [color-scheme:dark]" />
        </label>
        <button type="submit" className={ghostBtn}>{r.apply}</button>
      </form>
      <Loaded state={state}>
        {(page) => {
          if (!page) return <Notice>{r.notBuilt}</Notice>;
          const visible = page.items.map((m) => ({ m, href: metricHref(m.list, area), tag: metricTag(mode, m) })).filter(({ m, href }) => m.status === "no_data" || href);
          if (visible.length === 0) return <Notice>{r.empty}</Notice>;
          return (
            <ul className="grid gap-px overflow-hidden rounded-xl border border-white/10 bg-white/10 sm:grid-cols-2 lg:grid-cols-3">
              {visible.map(({ m, href, tag }) => (
                <li key={m.key} className="flex flex-col bg-ink-2 p-5">
                  <h2 className="text-[14px] text-white/75">{m.label}</h2>
                  <p className={`mt-2 tabular-nums leading-none ${m.status === "no_data" ? "text-[20px] text-white/70" : "text-[38px] font-light text-white"}`}>{show(m)}</p>
                  <p className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 font-mono text-[12px] text-white/60">
                    <span>{m.period}</span>
                    {tag && (
                      <span title={r.sampleTagHint} className="rounded border border-accent/40 px-1.5 py-0.5 text-accent">
                        {tag}<span className="sr-only">. {r.sampleTagHint}</span>
                      </span>
                    )}
                  </p>
                  <p className="mt-4 flex-1 text-[13.5px] leading-relaxed text-white/70"><span className="text-white/85">{r.source}:</span> {m.source}</p>
                  {m.status === "measured" && href && (
                    <Link href={href} className="mt-3 inline-flex min-h-11 w-fit items-center text-[14px] text-white underline decoration-white/40 underline-offset-4 hover:decoration-white">
                      {r.open} <span aria-hidden>&nbsp;→</span>
                    </Link>
                  )}
                </li>
              ))}
            </ul>
          );
        }}
      </Loaded>
      <p className="mt-6 text-[13px] text-white/60">{area === "portal" ? r.hoursNotePortal : r.hoursNote}</p>
    </>
  );
}
