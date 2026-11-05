import { site } from "@/content/site";
import { Section, Head } from "@/components/section";
import { Reveal, Stagger, StaggerItem } from "@/components/reveal";
import { HeroPanel } from "@/components/hero-panel";
import { Marquee } from "@/components/marquee";
import { Cases } from "@/components/cases";
import { ControlPanel } from "@/components/control-panel";
import { ResultsPanel } from "@/components/results-panel";
import { Particles } from "@/components/particles";
import { AssistantBar } from "@/components/assistant-bar";
import { Display } from "@/components/display";

const btn = "inline-flex min-h-11 items-center gap-2 rounded-full px-5 py-3 text-[14.5px] font-medium transition-transform active:scale-[0.97]";
const primary = `${btn} bg-paper text-ink hover:bg-white/90`;
const ghost = `${btn} border border-white/20 text-paper hover:bg-white/10`;

export default function Home() {
  const h = site.hero;
  return (
    <main>
      <header className="sticky top-0 z-20 border-b border-white/10 bg-ink/80 backdrop-blur">
        <div className="mx-auto flex max-w-6xl items-center justify-between px-4 py-4">
          <a href="#" className="text-[17px] font-medium tracking-tight">{site.name}</a>
          <nav className="hidden gap-7 text-[14px] text-white/65 md:flex">{site.nav.map((n) => <a key={n.href} href={n.href} className="hover:text-white">{n.label}</a>)}</nav>
          <a href="#contacto" className={`${primary} min-h-9 px-4 py-2 text-[13.5px]`}>{h.primary.label}</a>
        </div>
      </header>

      {/* Hero: título grande y liviano a la izquierda, el panel animado a la derecha. */}
      <section className="relative isolate overflow-hidden">
        <Particles />
        <div aria-hidden className="absolute inset-0 -z-10 opacity-[0.18] [background-image:radial-gradient(rgba(255,255,255,0.6)_1px,transparent_1px)] [background-size:22px_22px] [mask-image:radial-gradient(ellipse_at_top_left,black,transparent_60%)]" />
        <div className="mx-auto grid max-w-6xl gap-14 px-4 pb-20 pt-16 lg:grid-cols-[minmax(0,1.05fr)_minmax(0,0.95fr)] lg:items-center lg:py-28">
          <Reveal>
            <p className="font-mono text-[12px] tracking-wide text-white/45">{h.eyebrow}</p>
            <h1 className="mt-6 text-[40px] font-light leading-[1.04] tracking-[-0.015em] sm:text-[54px] xl:text-[60px]">
              <span className="block">{h.lineOne}</span>
              <span className="block text-white/45">{h.lineTwo}</span>
            </h1>
            <p className="mt-7 max-w-[30em] text-[18px] leading-relaxed text-white/65">{h.lede}</p>
            <div className="mt-9 flex flex-wrap gap-3">
              <a href={h.primary.href} className={primary}>{h.primary.label} <span aria-hidden>→</span></a>
              <a href={h.secondary.href} className={ghost}>{h.secondary.label}</a>
            </div>
            <p className="mt-10 font-mono text-[11px] uppercase tracking-widest text-white/35">{site.areas.join(" · ")}</p>
          </Reveal>
          <HeroPanel />
        </div>
      </section>

      <Marquee label={site.integrations.label} items={site.integrations.items} />

      <Section id="casos">
        <Head eyebrow={site.cases.eyebrow} title={site.cases.title} body={site.cases.body} />
        <Cases />
      </Section>

      <Section id="como" className="border-t border-white/10">
        <Head eyebrow={site.howItWorks.eyebrow} title={site.howItWorks.title} />
        <Stagger className="grid gap-10 border-t border-white/10 pt-8 md:grid-cols-3">
          {site.howItWorks.items.map((it) => (
            <StaggerItem key={it.n} className="text-[15.5px] leading-relaxed text-white/60">
              <span className="font-mono text-[12px] text-accent">{it.n}</span> <span className="text-white">{it.name}</span> {it.body}
            </StaggerItem>
          ))}
        </Stagger>
      </Section>

      <Display eyebrow={site.manifesto.eyebrow} title={site.manifesto.title} body={site.manifesto.body} cta={site.manifesto.cta} />

      <Section id="control" className="border-t border-white/10">
        <div className="grid gap-12 lg:grid-cols-2 lg:items-start">
          <div>
            <Head eyebrow={site.control.eyebrow} title={site.control.title} body={site.control.body} />
            <Stagger className="grid gap-7 sm:grid-cols-2">
              {site.control.items.map((c) => (
                <StaggerItem key={c.name} className="border-t border-white/10 pt-4">
                  <p className="text-[15px] text-white">{c.name}</p><p className="mt-2 text-[14.5px] leading-relaxed text-white/55">{c.body}</p>
                </StaggerItem>
              ))}
            </Stagger>
          </div>
          <Reveal delay={0.1}><ControlPanel /></Reveal>
        </div>
      </Section>

      <Section id="resultados" className="border-t border-white/10">
        <Head eyebrow={site.results.eyebrow} title={site.results.title} body={site.results.body} />
        <Reveal><ResultsPanel /></Reveal>
      </Section>

      <Section id="produccion" className="border-t border-white/10">
        <Head eyebrow={site.realCases.eyebrow} title={site.realCases.title} />
        <Stagger className="grid gap-6 md:grid-cols-2">
          {site.realCases.items.map((c) => (
            <StaggerItem key={c.sector} className="rounded-xl border border-white/10 bg-white/[0.03] p-6">
              <p className="font-mono text-[11px] uppercase tracking-widest text-accent">{c.sector}</p>
              <p className="mt-3 text-[17px] leading-relaxed text-white/85">{c.what}</p>
            </StaggerItem>
          ))}
        </Stagger>
        <p className="mt-4 font-mono text-[11px] text-white/35">{site.realCases.note}</p>
      </Section>

      <Section id="proceso" className="border-t border-white/10">
        <Head eyebrow={site.process.eyebrow} title={site.process.title} />
        <Stagger className="grid grid-cols-2 border-t border-white/10 lg:grid-cols-4">
          {site.process.steps.map((s, i) => (
            <StaggerItem key={s.name} className="border-b border-white/10 py-7 pr-5 lg:border-b-0">
              <span className="font-mono text-[12px] text-accent">0{i + 1}</span>
              <p className="mt-3 text-[24px] font-light">{s.name}</p>
              <p className="mt-2 text-[14.5px] leading-relaxed text-white/55">{s.body}</p>
            </StaggerItem>
          ))}
        </Stagger>
      </Section>

      <Section id="faq" className="border-t border-white/10">
        <Head eyebrow={site.faq.eyebrow} title={site.faq.title} />
        <dl className="divide-y divide-white/10 border-t border-white/10">
          {site.faq.items.map((f) => (
            <Reveal key={f.q}><div className="grid gap-2 py-5 sm:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]"><dt className="text-[16px] text-white">{f.q}</dt><dd className="text-[15.5px] leading-relaxed text-white/60">{f.a}</dd></div></Reveal>
          ))}
        </dl>
      </Section>

      <section id="contacto" className="scroll-mt-20 border-t border-white/10 bg-ink-2">
        <div className="mx-auto grid max-w-6xl gap-12 px-4 py-20 sm:py-28 lg:grid-cols-2">
          <Reveal>
            <p className="font-mono text-[12px] tracking-wide text-white/45">{site.contact.eyebrow}</p>
            <h2 className="mt-4 text-[34px] font-light leading-[1.08] sm:text-[46px]">{site.cta.title}</h2>
            <p className="mt-5 max-w-[30em] text-[18px] text-white/60">{site.cta.body}</p>
            <p className="mt-6 font-mono text-[13px] text-white/45">{site.email}</p>
          </Reveal>
          <Reveal delay={0.1}>
            <form action={`mailto:${site.email}`} method="post" encType="text/plain" className="grid gap-3">
              {(["name", "email", "company"] as const).map((k) => (
                <input key={k} name={k} type={k === "email" ? "email" : "text"} required={k !== "company"} placeholder={site.contact.fields[k]}
                  className="rounded-lg border border-white/15 bg-white/[0.04] px-4 py-3 text-[15px] placeholder:text-white/40 focus:border-white/40 focus:outline-none" />
              ))}
              <textarea name="message" rows={4} placeholder={site.contact.fields.message} className="rounded-lg border border-white/15 bg-white/[0.04] px-4 py-3 text-[15px] placeholder:text-white/40 focus:border-white/40 focus:outline-none" />
              <button type="submit" className={`${primary} w-fit`}>{site.contact.submit} <span aria-hidden>→</span></button>
            </form>
          </Reveal>
        </div>
        <footer className="mx-auto flex max-w-6xl flex-wrap justify-between gap-4 border-t border-white/10 px-4 py-8 text-[13px] text-white/40">
          <span>{site.name} · {site.tagline}</span><span>La Carlota, Córdoba · Argentina</span>
        </footer>
      </section>
      <AssistantBar />
    </main>
  );
}
