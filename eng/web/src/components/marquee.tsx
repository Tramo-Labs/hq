/** Fila de integraciones en movimiento continuo (CSS puro; se detiene con reduced motion). */
export function Marquee({ items, label }: { items: string[]; label: string }) {
  const row = [...items, ...items];
  return (
    <div className="border-y border-white/10 py-5">
      <div className="mx-auto flex max-w-6xl items-center gap-8 px-4">
        <span className="shrink-0 font-mono text-[11px] uppercase tracking-widest text-white/40">{label}</span>
        <div className="relative flex-1 overflow-hidden [mask-image:linear-gradient(to_right,transparent,black_10%,black_90%,transparent)]">
          <div className="marquee flex w-max gap-10">
            {row.map((it, i) => <span key={i} className="whitespace-nowrap text-[15px] text-white/55">{it}</span>)}
          </div>
        </div>
      </div>
    </div>
  );
}
