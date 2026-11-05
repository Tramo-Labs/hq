import { site } from "@/content/site";

export function ControlPanel() {
  const p = site.control.panel;
  return (
    <figure className="overflow-hidden rounded-lg border border-line bg-paper">
      <div className="border-b border-line bg-pearl px-4 py-2.5 font-mono text-xs text-ink-soft">{p.title}</div>
      <div className="divide-y divide-line px-4">
        {p.permissions.map((r) => (
          <div key={r.system} className="flex items-center justify-between py-3 text-sm">
            <span>{r.system}</span>
            <span className={`rounded px-2 py-0.5 font-mono text-[11px] ${r.on ? "bg-ink text-paper" : "border border-line text-ink-soft"}`}>{r.on ? "✓ " : "× "}{r.level}</span>
          </div>
        ))}
      </div>
      <figcaption className="border-t border-line px-4 py-2 font-mono text-[11px] text-ink-soft">{p.note}</figcaption>
    </figure>
  );
}
