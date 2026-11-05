"use client";
import { motion, useReducedMotion } from "motion/react";
import { site } from "@/content/site";
import { LogRow, Window } from "@/components/log";
import { Counter } from "@/components/counter";
import { motionTokens, springs } from "@/lib/motion-tokens";

/** El visual del hero: números del día y el log que aparece línea por línea. Datos de ejemplo. */
export function HeroPanel() {
  const p = site.hero.panel;
  const reduced = useReducedMotion();
  return (
    <motion.div initial={{ opacity: 0, y: reduced ? 0 : motionTokens.distance.xl }} animate={{ opacity: 1, y: 0 }} transition={{ ...springs.gentle, delay: 0.25 }}>
      <Window title={p.title} caption={p.note}>
        <div className="grid grid-cols-3 divide-x divide-white/10 border-b border-white/10">
          {p.stats.map((s) => (
            <div key={s.label} className="px-4 py-4">
              <p className="text-[12px] text-white/50">{s.label}</p>
              <p className="mt-1 text-[26px] font-light tabular-nums text-white"><Counter value={s.value} /></p>
            </div>
          ))}
        </div>
        <div className="divide-y divide-white/10 px-4">
          {p.log.map((l, i) => (
            <motion.div key={l.time + l.text} initial={{ opacity: 0, x: reduced ? 0 : -motionTokens.distance.sm }} animate={{ opacity: 1, x: 0 }} transition={{ ...springs.snappy, delay: 0.6 + i * 0.35 }}>
              <LogRow time={l.time} text={l.text} tag={l.tag as never} />
            </motion.div>
          ))}
        </div>
      </Window>
    </motion.div>
  );
}
