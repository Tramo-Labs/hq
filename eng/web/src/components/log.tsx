import type { Tag } from "@/content/site";

const tagStyle: Record<Tag, string> = {
  ok: "border border-white/15 text-white/60",
  pendiente: "border border-dashed border-accent text-accent",
  aprobada: "bg-white text-ink",
};

export function LogRow({ time, text, tag }: { time: string; text: string; tag: Tag }) {
  return (
    <div className={`flex items-center gap-3 py-2.5 text-[13.5px] ${tag === "pendiente" ? "bg-accent/10" : ""}`}>
      <span className="font-mono text-[11px] tabular-nums text-white/40">{time}</span>
      <span className="flex-1 text-white/85">{text}</span>
      <span className={`rounded px-2 py-0.5 font-mono text-[11px] ${tagStyle[tag]}`}>{tag}</span>
    </div>
  );
}

/** Marco de ventana oscuro, como las capturas de producto. */
export function Window({ title, children, caption, className = "" }: { title: string; children: React.ReactNode; caption?: string; className?: string }) {
  return (
    <figure className={`overflow-hidden rounded-xl border border-white/10 bg-[#111312] shadow-[0_30px_80px_-30px_rgba(0,0,0,0.9)] ${className}`}>
      <div className="flex items-center gap-2 border-b border-white/10 px-4 py-2.5">
        <span className="size-2.5 rounded-full bg-white/15" /><span className="size-2.5 rounded-full bg-white/15" /><span className="size-2.5 rounded-full bg-white/15" />
        <span className="ml-3 font-mono text-[11.5px] text-white/50">{title}</span>
      </div>
      {children}
      {caption && <figcaption className="border-t border-white/10 px-4 py-2 font-mono text-[11px] text-white/35">{caption}</figcaption>}
    </figure>
  );
}
