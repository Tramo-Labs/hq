import { site } from "@/content/site";

/** El panel de resultados: cuatro cifras y, debajo, cómo se midió cada una. Datos de ejemplo. */
export function ResultsPanel() {
  const p = site.results.panel;
  return (
    <figure className="overflow-hidden rounded-lg border border-line bg-paper">
      <div className="border-b border-line bg-pearl px-4 py-2.5 font-mono text-xs text-ink-soft">{p.title}</div>
      <div className="grid grid-cols-2 divide-x divide-y divide-line lg:grid-cols-4 lg:divide-y-0">
        {p.stats.map((s) => (
          <div key={s.label} className="p-5">
            <p className="text-sm text-ink-soft">{s.label}</p>
            <p className="mt-2 text-4xl font-light tabular-nums">{s.value}</p>
            <p className="mt-1 text-xs text-ink-soft">{s.sub}</p>
          </div>
        ))}
      </div>
      <table className="w-full border-t border-line text-sm">
        <thead><tr className="bg-pearl font-mono text-[11px] uppercase tracking-widest text-ink-soft"><th className="px-4 py-2 text-left font-normal">Métrica</th><th className="px-4 py-2 text-left font-normal">Cómo se midió</th></tr></thead>
        <tbody className="divide-y divide-line">
          {p.provenance.map((r) => <tr key={r.metric}><td className="px-4 py-3 align-top">{r.metric}</td><td className="px-4 py-3 text-ink-soft">{r.how}</td></tr>)}
        </tbody>
      </table>
      <figcaption className="border-t border-line px-4 py-2 font-mono text-[11px] text-ink-soft">{p.note}</figcaption>
    </figure>
  );
}
