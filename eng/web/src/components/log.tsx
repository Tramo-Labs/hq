import type { Tag } from "@/content/site";

const tagStyle: Record<Tag, string> = {
  ok: "border border-line text-ink-soft",
  pendiente: "border border-dashed border-accent text-accent",
  aprobada: "bg-ink text-paper",
};

export function LogRow({ time, text, tag, dark = false }: { time: string; text: string; tag: Tag; dark?: boolean }) {
  return (
    <div className={`flex items-center gap-3 py-2.5 text-sm ${tag === "pendiente" ? (dark ? "bg-white/5" : "bg-amber-50/60") : ""}`}>
      <span className={`font-mono text-xs ${dark ? "text-white/50" : "text-ink-soft"}`}>{time}</span>
      <span className="flex-1">{text}</span>
      <span className={`rounded px-2 py-0.5 font-mono text-[11px] ${dark && tag === "aprobada" ? "bg-paper text-ink" : tagStyle[tag]}`}>{tag}</span>
    </div>
  );
}
