import { site } from "@/content/site";
import { Window } from "@/components/log";
import { Stagger, StaggerItem } from "@/components/reveal";

export function ControlPanel() {
  const p = site.control.panel;
  return (
    <Window title={p.title} caption={p.note}>
      <Stagger className="divide-y divide-white/10 px-4">
        {p.permissions.map((r) => (
          <StaggerItem key={r.system} className="flex items-center justify-between py-3 text-[14px]">
            <span className="text-white/85">{r.system}</span>
            <span className={`rounded px-2 py-0.5 font-mono text-[11px] ${r.on ? "bg-white text-ink" : "border border-white/15 text-white/50"}`}>{r.on ? "✓ " : "× "}{r.level}</span>
          </StaggerItem>
        ))}
      </Stagger>
    </Window>
  );
}
