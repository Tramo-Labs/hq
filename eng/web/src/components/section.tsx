import type { ReactNode } from "react";
import { Reveal } from "@/components/reveal";

export function Section({ id, children, className = "" }: { id: string; children: ReactNode; className?: string }) {
  return (
    <section id={id} className={`scroll-mt-20 ${className}`}>
      <div className="mx-auto max-w-6xl px-4 py-20 sm:py-28">{children}</div>
    </section>
  );
}

/** Cabecera de sección: rótulo mono, título grande y liviano, una frase. */
export function Head({ eyebrow, title, body }: { eyebrow: string; title: string; body?: string }) {
  return (
    <Reveal className="mb-12 max-w-3xl">
      <p className="font-mono text-[12px] tracking-wide text-white/45">{eyebrow}</p>
      <h2 className="mt-4 text-[34px] font-light leading-[1.08] tracking-[-0.012em] sm:text-[46px]">{title}</h2>
      {body && <p className="mt-5 max-w-[32em] text-[18px] leading-relaxed text-white/60">{body}</p>}
    </Reveal>
  );
}
