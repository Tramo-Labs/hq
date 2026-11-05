import { site } from "@/content/site";
import { Section, Head } from "@/components/section";
import { HeroPanel } from "@/components/hero-panel";
import { Cases } from "@/components/cases";
import { ControlPanel } from "@/components/control-panel";
import { ResultsPanel } from "@/components/results-panel";

const btn = "inline-flex min-h-11 items-center gap-2 rounded-md px-5 py-3 text-sm font-medium";

export default function Home() {
  const h = site.hero;
  return (
    <main>
      {/* Hero oscuro, texto a la izquierda y el panel a la derecha. */}
      <section className="relative isolate overflow-hidden bg-ink text-paper">
        <div aria-hidden className="absolute inset-0 -z-10 opacity-20 [background-image:radial-gradient(rgba(255,255,255,0.5)_1px,transparent_1px)] [background-size:22px_22px] [mask-image:linear-gradient(to_right,black,transparent_70%)]" />
        <header className="mx-auto flex max-w-6xl items-center justify-between px-4 py-5">
          <span className="text-lg font-semibold tracking-tight">{site.name}</span>
          <nav className="hidden gap-6 text-sm text-white/70 md:flex">{site.nav.map((n) => <a key={n.href} href={n.href} className="hover:text-white">{n.label}</a>)}</nav>
          <a href={h.primary.href} className={`${btn} bg-paper text-ink`}>{h.primary.label}</a>
        </header>
        <div className="mx-auto grid max-w-6xl gap-12 px-4 pb-16 pt-10 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)] lg:items-center lg:py-24">
          <div>
            <p className="font-mono text-xs tracking-wide text-white/55">{h.eyebrow}</p>
            <h1 className="mt-6 text-4xl font-light leading-[1.05] tracking-tight sm:text-5xl xl:text-[56px]">
              <span className="block">{h.lineOne}</span>
              <span className="block text-white/55">{h.lineTwo}</span>
            </h1>
            <p className="mt-6 max-w-[30em] text-lg leading-relaxed text-white/75">{h.lede}</p>
            <div className="mt-9 flex flex-wrap gap-3">
              <a href={h.primary.href} className={`${btn} bg-paper text-ink`}>{h.primary.label} <span aria-hidden>→</span></a>
              <a href={h.secondary.href} className={`${btn} border border-white/25 text-paper hover:bg-white/10`}>{h.secondary.label}</a>
            </div>
            <p className="mt-10 font-mono text-xs uppercase tracking-widest text-white/45">{site.areas.join(" · ")}</p>
          </div>
          <HeroPanel />
        </div>
      </section>

      <Section id="casos">
        <Head eyebrow={site.cases.eyebrow} title={site.cases.title} body={site.cases.body} />
        <Cases />
      </Section>

      <Section id="control" tone="pearl">
        <div className="grid gap-12 lg:grid-cols-2 lg:items-start">
          <div>
            <Head eyebrow={site.control.eyebrow} title={site.control.title} body={site.control.body} />
            <dl className="grid gap-6 sm:grid-cols-2">
              {site.control.items.map((c) => (
                <div key={c.name} className="border-t border-line pt-4"><dt className="font-mono text-xs uppercase tracking-widest text-accent">{c.name}</dt><dd className="mt-2 text-ink-soft">{c.body}</dd></div>
              ))}
            </dl>
          </div>
          <ControlPanel />
        </div>
      </Section>

      <Section id="resultados">
        <Head eyebrow={site.results.eyebrow} title={site.results.title} body={site.results.body} />
        <ResultsPanel />
      </Section>

      <Section id="proceso" tone="pearl">
        <Head eyebrow={site.process.eyebrow} title={site.process.title} />
        <ol className="grid grid-cols-2 border-t border-line lg:grid-cols-4">
          {site.process.steps.map((s, i) => (
            <li key={s.name} className="border-b border-line py-6 pr-4 lg:border-b-0"><span className="font-mono text-xs text-accent">0{i + 1}</span><p className="mt-3 text-2xl font-light">{s.name}</p><p className="mt-2 text-ink-soft">{s.body}</p></li>
          ))}
        </ol>
        <a href="#contacto" className={`${btn} mt-10 bg-ink text-paper`}>{site.process.cta}</a>
      </Section>

      <Section id="faq">
        <Head eyebrow={site.faq.eyebrow} title={site.faq.title} />
        <dl className="divide-y divide-line border-t border-line">
          {site.faq.items.map((f) => (
            <div key={f.q} className="grid gap-2 py-5 sm:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]"><dt className="font-medium">{f.q}</dt><dd className="text-ink-soft">{f.a}</dd></div>
          ))}
        </dl>
      </Section>

      {/* Banda final oscura con el formulario. */}
      <section id="contacto" className="scroll-mt-16 bg-ink text-paper">
        <div className="mx-auto grid max-w-6xl gap-12 px-4 py-16 sm:py-24 lg:grid-cols-2">
          <div>
            <p className="font-mono text-xs uppercase tracking-widest text-white/55">{site.contact.eyebrow}</p>
            <h2 className="mt-4 text-3xl font-light leading-tight sm:text-4xl">{site.cta.title}</h2>
            <p className="mt-5 max-w-[30em] text-lg text-white/75">{site.cta.body}</p>
            <p className="mt-6 font-mono text-sm text-white/55">{site.email}</p>
          </div>
          <form action={`mailto:${site.email}`} method="post" encType="text/plain" className="grid gap-3">
            <input name="nombre" required placeholder={site.contact.fields.name} className="rounded-md border border-white/20 bg-white/5 px-4 py-3 placeholder:text-white/45" />
            <input name="email" type="email" required placeholder={site.contact.fields.email} className="rounded-md border border-white/20 bg-white/5 px-4 py-3 placeholder:text-white/45" />
            <input name="empresa" placeholder={site.contact.fields.company} className="rounded-md border border-white/20 bg-white/5 px-4 py-3 placeholder:text-white/45" />
            <textarea name="mensaje" rows={4} placeholder={site.contact.fields.message} className="rounded-md border border-white/20 bg-white/5 px-4 py-3 placeholder:text-white/45" />
            <button type="submit" className={`${btn} w-fit bg-paper text-ink`}>{site.contact.submit} <span aria-hidden>→</span></button>
          </form>
        </div>
        <footer className="mx-auto flex max-w-6xl flex-wrap justify-between gap-4 border-t border-white/10 px-4 py-8 text-sm text-white/55">
          <span>{site.name} · {site.tagline}</span><span>© 2026</span>
        </footer>
      </section>
    </main>
  );
}
