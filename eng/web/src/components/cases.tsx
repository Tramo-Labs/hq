"use client";
import { useState } from "react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { site, type Tag } from "@/content/site";
import { LogRow, Window } from "@/components/log";
import { motionTokens, springs } from "@/lib/motion-tokens";

/** Pestañas con subrayado (como nexus.inc) y tres columnas por caso (como LFT). */
export function Cases() {
  const c = site.cases;
  const [i, setI] = useState(0);
  const reduced = useReducedMotion();
  const item = c.items[i];
  return (
    <div>
      <div role="tablist" className="grid grid-cols-3 border-t border-white/10">
        {c.items.map((it, k) => (
          <button key={it.name} role="tab" aria-selected={k === i} onClick={() => setI(k)}
            className={`relative py-4 text-left text-[15px] transition-colors sm:text-[17px] ${k === i ? "text-white" : "text-white/45 hover:text-white/80"}`}>
            {k === i && <motion.span layoutId="tab-line" className="absolute -top-px left-0 h-px w-full bg-white" transition={springs.snappy} />}
            {it.name}
          </button>
        ))}
      </div>
      <AnimatePresence mode="wait">
        <motion.div key={i} initial={{ opacity: 0, y: reduced ? 0 : motionTokens.distance.sm }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0 }} transition={{ duration: motionTokens.duration.normal, ease: motionTokens.easing.smooth }}
          className="mt-8 grid gap-6 lg:grid-cols-3">
          <div>
            <p className="font-mono text-[11px] uppercase tracking-widest text-white/40"><span className="text-accent">01</span> {c.columns[0]}</p>
            <div className="mt-4 rounded-lg border border-white/10 bg-white/[0.03] p-4">
              <p className="text-[13.5px] text-white">{item.need.from}</p>
              <p className="font-mono text-[11px] text-white/40">{item.need.via}</p>
              <p className="mt-3 text-[15px] text-white/85">{item.need.text}</p>
            </div>
            <div className="mt-3 ml-6 rounded-lg border border-white/10 p-4">
              <p className="text-[13.5px] text-white">{site.name}</p>
              <p className="mt-2 text-[15px] text-white/70">{item.reply}</p>
            </div>
          </div>
          <div>
            <p className="font-mono text-[11px] uppercase tracking-widest text-white/40"><span className="text-accent">02</span> {c.columns[1]}</p>
            <Window title={`${item.slug} · ficha`} className="mt-4">
              <dl className="divide-y divide-white/10">
                {item.sheet.map(([k, v]) => (
                  <div key={k} className="grid grid-cols-[5.5rem_1fr] gap-3 px-4 py-3 text-[14px]"><dt className="font-mono text-[11px] uppercase text-white/40">{k}</dt><dd className="text-white/85">{v}</dd></div>
                ))}
              </dl>
            </Window>
          </div>
          <div>
            <p className="font-mono text-[11px] uppercase tracking-widest text-white/40"><span className="text-accent">03</span> {c.columns[2]}</p>
            <Window title={`${item.slug} · hoy`} className="mt-4" caption="Vista ilustrativa con datos de ejemplo">
              <div className="divide-y divide-white/10 px-4">
                {item.live.map((l) => <LogRow key={l.time + l.text} time={l.time} text={l.text} tag={l.tag as Tag} />)}
              </div>
            </Window>
          </div>
        </motion.div>
      </AnimatePresence>
    </div>
  );
}
