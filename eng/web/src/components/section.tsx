import type { ReactNode } from "react";

export function Section({ id, tone = "paper", children }: { id: string; tone?: "paper" | "pearl"; children: ReactNode }) {
  return (
    <section id={id} className={`scroll-mt-16 border-t border-line ${tone === "pearl" ? "bg-pearl" : ""}`}>
      <div className="mx-auto max-w-6xl px-4 py-16 sm:py-24">{children}</div>
    </section>
  );
}

export function Head({ eyebrow, title, body }: { eyebrow: string; title: string; body?: string }) {
  return (
    <div className="mb-10 max-w-2xl">
      <p className="mb-3 font-mono text-xs uppercase tracking-widest text-ink-soft">{eyebrow}</p>
      <h2 className="text-3xl font-semibold leading-tight sm:text-4xl">{title}</h2>
      {body && <p className="mt-4 text-lg text-ink-soft">{body}</p>}
    </div>
  );
}
