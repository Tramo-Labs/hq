"use client";
import { useState } from "react";
import { site, type Tag } from "@/content/site";
import { LogRow } from "@/components/log";

/** Tres casos, uno por pestaña: la necesidad tal como llega, la ficha de la automatización y cómo se ve en producción. */
export function Cases() {
  const c = site.cases;
  const [i, setI] = useState(0);
  const item = c.items[i];
  return (
    <div>
      <div className="mb-6 flex flex-wrap items-center gap-2">
        <span className="mr-2 font-mono text-xs uppercase tracking-widest text-ink-soft">Ejemplos</span>
        {c.items.map((it, k) => (
          <button key={it.name} onClick={() => setI(k)} aria-pressed={k === i}
            className={`rounded-full border px-4 py-1.5 text-sm transition ${k === i ? "border-ink bg-ink text-paper" : "border-line hover:border-ink-soft"}`}>
            {it.name}
          </button>
        ))}
      </div>
      <div className="grid gap-px overflow-hidden rounded-lg border border-line bg-line lg:grid-cols-3">
        <div className="bg-paper p-5">
          <p className="font-mono text-xs uppercase tracking-widest text-ink-soft"><span className="text-accent">01</span> {c.columns[0]}</p>
          <div className="mt-4 rounded-md border border-line bg-pearl p-4">
            <p className="text-sm font-medium">{item.need.from}</p>
            <p className="font-mono text-[11px] text-ink-soft">{item.need.via}</p>
            <p className="mt-3">{item.need.text}</p>
          </div>
          <div className="mt-3 ml-6 rounded-md border border-line p-4">
            <p className="text-sm font-medium">{site.name}</p>
            <p className="mt-2 text-ink-soft">{item.reply}</p>
          </div>
        </div>
        <div className="bg-paper p-5">
          <p className="font-mono text-xs uppercase tracking-widest text-ink-soft"><span className="text-accent">02</span> {c.columns[1]}</p>
          <dl className="mt-4 divide-y divide-line rounded-md border border-line">
            {item.sheet.map(([k, v]) => (
              <div key={k} className="grid grid-cols-[6rem_1fr] gap-3 px-4 py-3 text-sm"><dt className="font-mono text-xs uppercase text-ink-soft">{k}</dt><dd>{v}</dd></div>
            ))}
          </dl>
        </div>
        <div className="bg-paper p-5">
          <p className="font-mono text-xs uppercase tracking-widest text-ink-soft"><span className="text-accent">03</span> {c.columns[2]}</p>
          <div className="mt-4 divide-y divide-line rounded-md border border-line px-4">
            {item.live.map((l) => <LogRow key={l.time + l.text} time={l.time} text={l.text} tag={l.tag as Tag} />)}
          </div>
          <p className="mt-3 font-mono text-[11px] text-ink-soft">Vista ilustrativa con datos de ejemplo</p>
        </div>
      </div>
    </div>
  );
}
