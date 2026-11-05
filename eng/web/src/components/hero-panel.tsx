import { site } from "@/content/site";
import { LogRow } from "@/components/log";

/** El visual del hero: un panel de automatización con sus números del día y el log. Datos de ejemplo. */
export function HeroPanel() {
  const p = site.hero.panel;
  return (
    <figure className="overflow-hidden rounded-lg border border-white/10 bg-white/[0.04] text-white shadow-[0_40px_120px_-30px_rgba(0,0,0,0.8)] backdrop-blur">
      <div className="flex items-center gap-2 border-b border-white/10 px-4 py-2.5 font-mono text-xs text-white/60">
        <span className="size-2 rounded-full bg-accent" /> {p.title}
      </div>
      <div className="grid grid-cols-3 divide-x divide-white/10 border-b border-white/10">
        {p.stats.map((s) => (
          <div key={s.label} className="px-4 py-4">
            <p className="text-xs text-white/55">{s.label}</p>
            <p className="mt-1 text-2xl font-light tabular-nums">{s.value}</p>
          </div>
        ))}
      </div>
      <div className="divide-y divide-white/10 px-4">
        {p.log.map((l) => <LogRow key={l.time + l.text} time={l.time} text={l.text} tag={l.tag as never} dark />)}
      </div>
      <figcaption className="border-t border-white/10 px-4 py-2 font-mono text-[11px] text-white/45">{p.note}</figcaption>
    </figure>
  );
}
