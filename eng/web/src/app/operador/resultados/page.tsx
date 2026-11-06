import type { Metadata } from "next";
import Link from "next/link";
import { app } from "@/content/app";
import { PageHead, formatMoney } from "@/components/app/ui";
import { listMetrics } from "@/lib/api";
import type { Metric } from "@/lib/api";

export const metadata: Metadata = { title: app.results.eyebrow };

const r = app.results;

function show(m: Metric) {
  if (m.value === null) return r.noData;
  if (m.unit === "percent") return `${m.value} %`;
  if (m.unit === "days") return r.days(m.value);
  if (m.unit === "ars") return formatMoney(m.value);
  return m.value.toLocaleString("es-AR");
}

export default async function Results() {
  const { items } = await listMetrics();
  return (
    <>
      <PageHead eyebrow={r.eyebrow} title={r.title} body={r.body} />
      <ul className="grid gap-px overflow-hidden rounded-xl border border-white/10 bg-white/10 sm:grid-cols-2 lg:grid-cols-3">
        {items.map((m) => {
          const empty = m.value === null;
          return (
            <li key={m.key} className="flex flex-col bg-ink-2 p-5">
              <h2 className="text-[14px] text-white/75">{r.labels[m.key]}</h2>
              <p className={`mt-2 tabular-nums leading-none ${empty ? "text-[20px] text-white/70" : "text-[38px] font-light text-white"}`}>{show(m)}</p>
              <p className="mt-2 font-mono text-[12px] text-white/60">{m.period}</p>
              <p className="mt-4 flex-1 text-[13.5px] leading-relaxed text-white/70"><span className="text-white/85">{r.source}:</span> {m.source}</p>
              {m.href && !empty && (
                <Link href={m.href} className="mt-4 inline-flex min-h-11 w-fit items-center text-[14px] text-white underline decoration-white/40 underline-offset-4 hover:decoration-white">
                  {r.open} <span aria-hidden>&nbsp;→</span>
                </Link>
              )}
            </li>
          );
        })}
      </ul>
      <p className="mt-6 text-[13px] text-white/60">{r.hoursNote}</p>
    </>
  );
}
