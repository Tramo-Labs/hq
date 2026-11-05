import { Reveal } from "@/components/reveal";

/** Sección de declaración, al estilo del bloque rojo de DANKA: una frase enorme sobre fondo de acento. */
export function Display({ eyebrow, title, body, cta }: { eyebrow: string; title: string; body: string; cta: { label: string; href: string } }) {
  return (
    <section className="relative isolate overflow-hidden bg-accent text-ink">
      <div aria-hidden className="absolute -right-24 top-10 -z-10 h-[420px] w-[420px] rounded-[48px] bg-ink/10 sm:h-[560px] sm:w-[560px]" />
      <div className="mx-auto max-w-6xl px-4 py-24 sm:py-32">
        <Reveal>
          <p className="font-mono text-[12px] uppercase tracking-[0.2em] text-ink/60">{eyebrow}</p>
          <h2 className="mt-6 max-w-[11em] text-[44px] font-medium uppercase leading-[0.95] tracking-[-0.02em] sm:text-[84px]">{title}</h2>
          <p className="mt-8 max-w-[28em] text-[18px] leading-relaxed text-ink/75">{body}</p>
          <a href={cta.href} className="mt-10 inline-flex min-h-11 items-center gap-2 rounded-full bg-ink px-6 py-3 text-[14.5px] font-medium text-paper">{cta.label} <span aria-hidden>→</span></a>
        </Reveal>
      </div>
    </section>
  );
}
