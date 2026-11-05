import { site } from "@/content/site";
import { Window } from "@/components/log";
import { Counter } from "@/components/counter";

/** El panel de resultados: cuatro cifras que cuentan al entrar y, debajo, cómo se midió cada una. Datos de ejemplo. */
export function ResultsPanel() {
  const p = site.results.panel;
  return (
    <Window title={p.title} caption={p.note}>
      <div className="grid grid-cols-2 divide-x divide-y divide-white/10 lg:grid-cols-4 lg:divide-y-0">
        {p.stats.map((s) => (
          <div key={s.label} className="p-5">
            <p className="text-[13px] text-white/50">{s.label}</p>
            <p className="mt-2 text-[40px] font-light leading-none tabular-nums text-white"><Counter value={s.value} /></p>
            <p className="mt-2 text-[12px] text-white/45">{s.sub}</p>
          </div>
        ))}
      </div>
      <table className="w-full border-t border-white/10 text-[14px]">
        <thead><tr className="font-mono text-[11px] uppercase tracking-widest text-white/40"><th className="px-4 py-2.5 text-left font-normal">Métrica</th><th className="px-4 py-2.5 text-left font-normal">Cómo se midió</th></tr></thead>
        <tbody className="divide-y divide-white/10">
          {p.provenance.map((r) => <tr key={r.metric}><td className="px-4 py-3 align-top text-white/85">{r.metric}</td><td className="px-4 py-3 text-white/55">{r.how}</td></tr>)}
        </tbody>
      </table>
    </Window>
  );
}
