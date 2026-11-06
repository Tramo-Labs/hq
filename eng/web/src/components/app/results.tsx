import Link from "next/link";
import { app } from "@/content/app";
import { formatMoney } from "@/components/app/ui";
import { metricHref } from "@/lib/api";
import type { Metric } from "@/lib/api";

const r = app.results;
type Area = "portal" | "console";

function show(value: number | null, unit: Metric["unit"]) {
  if (value === null) return r.noData;
  if (unit === "percent") return `${value} %`;
  if (unit === "days") return r.days(value);
  if (unit === "ars") return formatMoney(value);
  return value.toLocaleString("es-AR");
}

function OpenList({ href }: { href: string }) {
  return (
    <Link href={href} className="mt-3 inline-flex min-h-11 w-fit items-center text-[14px] text-white underline decoration-white/40 underline-offset-4 hover:decoration-white">
      {r.open} <span aria-hidden>&nbsp;→</span>
    </Link>
  );
}

/**
 * The results panel, shared by the client portal and the operator console. A number whose
 * list cannot be opened from this area is left out (spec section 4); "no data yet" still
 * shows, because it is not a number.
 */
export function ResultsPanel({ metrics, area, tenantId }: { metrics: Metric[]; area: Area; tenantId: string }) {
  const visible = metrics
    .map((m) => ({
      m,
      href: metricHref(m.list, area, tenantId),
      rows: m.byAccount
        ?.map((a) => ({ ...a, href: metricHref(a.list, area, tenantId) }))
        .filter((a) => a.value === null || a.href),
    }))
    .filter(({ m, href, rows }) => (rows ? rows.length > 0 : m.value === null || href));

  if (visible.length === 0) return <p className="rounded-xl border border-white/10 p-6 text-[15px] text-white/70">{r.empty}</p>;

  return (
    <ul className="grid gap-px overflow-hidden rounded-xl border border-white/10 bg-white/10 sm:grid-cols-2 lg:grid-cols-3">
      {visible.map(({ m, href, rows }) => (
        <li key={m.key} className="flex flex-col bg-ink-2 p-5">
          <h2 className="text-[14px] text-white/75">{r.labels[m.key]}</h2>
          {rows ? (
            <>
              <p className="mt-2 font-mono text-[12px] text-white/60">{r.byAccount} · {m.period}</p>
              <ul className="mt-3 divide-y divide-white/10 border-y border-white/10">
                {rows.map((a) => (
                  <li key={a.account} className="py-2.5">
                    <div className="flex flex-wrap items-baseline justify-between gap-x-3">
                      <span className="text-[14px] text-white/85">{a.account}</span>
                      <span className={`tabular-nums ${a.value === null ? "text-[14px] text-white/70" : "text-[18px] text-white"}`}>{show(a.value, m.unit)}</span>
                    </div>
                    {a.value !== null && a.href && <OpenList href={a.href} />}
                  </li>
                ))}
              </ul>
            </>
          ) : (
            <>
              <p className={`mt-2 tabular-nums leading-none ${m.value === null ? "text-[20px] text-white/70" : "text-[38px] font-light text-white"}`}>{show(m.value, m.unit)}</p>
              <p className="mt-2 font-mono text-[12px] text-white/60">{m.period}</p>
            </>
          )}
          <p className="mt-4 flex-1 text-[13.5px] leading-relaxed text-white/70"><span className="text-white/85">{r.source}:</span> {m.source}</p>
          {!rows && m.value !== null && href && <OpenList href={href} />}
        </li>
      ))}
    </ul>
  );
}
