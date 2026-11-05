import { site } from "@/content/site";
import { Section, Head } from "@/components/section";

const btn = "inline-flex items-center rounded-md px-5 py-3 text-sm font-medium";

export default function Home() {
  return (
    <main>
      <header className="mx-auto flex max-w-6xl items-center justify-between px-4 py-5">
        <span className="font-semibold">{site.name}</span>
        <nav className="hidden gap-6 text-sm text-ink-soft sm:flex">
          <a href="#servicios">Servicios</a><a href="#proceso">Proceso</a><a href="#control">Control</a><a href="#contacto">Contacto</a>
        </nav>
      </header>

      <section className="mx-auto max-w-6xl px-4 pb-20 pt-12 sm:pt-24">
        <h1 className="max-w-3xl text-4xl font-semibold leading-tight sm:text-6xl">{site.hero.title}</h1>
        <p className="mt-6 max-w-2xl text-lg text-ink-soft">{site.hero.body}</p>
        <div className="mt-8 flex flex-wrap gap-3">
          <a href={site.hero.primary.href} className={`${btn} bg-ink text-paper`}>{site.hero.primary.label}</a>
          <a href={site.hero.secondary.href} className={`${btn} border border-line`}>{site.hero.secondary.label}</a>
        </div>
        <p className="mt-10 font-mono text-xs uppercase tracking-widest text-ink-soft">{site.areas.join(" · ")}</p>
      </section>

      <Section id="servicios">
        <Head eyebrow={site.services.eyebrow} title={site.services.title} />
        <div className="grid gap-px overflow-hidden rounded-lg border border-line bg-line sm:grid-cols-2">
          {site.services.items.map((s) => (
            <div key={s.name} className="bg-paper p-6"><h3 className="font-semibold">{s.name}</h3><p className="mt-2 text-ink-soft">{s.body}</p></div>
          ))}
        </div>
      </Section>

      <Section id="proceso" tone="pearl">
        <Head eyebrow={site.process.eyebrow} title={site.process.title} />
        <ol className="grid gap-6 sm:grid-cols-4">
          {site.process.steps.map((s, i) => (
            <li key={s.name}><p className="font-mono text-xs text-ink-soft">0{i + 1}</p><h3 className="mt-2 font-semibold">{s.name}</h3><p className="mt-2 text-ink-soft">{s.body}</p></li>
          ))}
        </ol>
      </Section>

      <Section id="control">
        <Head eyebrow={site.control.eyebrow} title={site.control.title} />
        <div className="grid gap-6 sm:grid-cols-2">
          {site.control.items.map((c) => (
            <div key={c.name} className="rounded-lg border border-line p-6"><h3 className="font-semibold">{c.name}</h3><p className="mt-2 text-ink-soft">{c.body}</p></div>
          ))}
        </div>
      </Section>

      <Section id="resultados" tone="pearl">
        <Head eyebrow={site.results.eyebrow} title={site.results.title} body={site.results.body} />
        <p className="font-mono text-xs text-ink-soft">{site.results.note}</p>
      </Section>

      <Section id="faq">
        <Head eyebrow="Preguntas" title="Lo que suelen preguntar." />
        <dl className="divide-y divide-line">
          {site.faq.map((f) => (
            <div key={f.q} className="py-5"><dt className="font-semibold">{f.q}</dt><dd className="mt-2 text-ink-soft">{f.a}</dd></div>
          ))}
        </dl>
      </Section>

      <Section id="contacto" tone="pearl">
        <Head eyebrow={site.contact.eyebrow} title={site.contact.title} body={site.contact.body} />
        <form action={`mailto:${site.email}`} method="post" encType="text/plain" className="grid max-w-xl gap-4">
          <input name="nombre" required placeholder={site.contact.fields.name} className="rounded-md border border-line bg-paper px-4 py-3" />
          <input name="email" type="email" required placeholder={site.contact.fields.email} className="rounded-md border border-line bg-paper px-4 py-3" />
          <input name="empresa" placeholder={site.contact.fields.company} className="rounded-md border border-line bg-paper px-4 py-3" />
          <textarea name="mensaje" rows={4} placeholder={site.contact.fields.message} className="rounded-md border border-line bg-paper px-4 py-3" />
          <button type="submit" className={`${btn} w-fit bg-ink text-paper`}>{site.contact.submit}</button>
        </form>
      </Section>

      <footer className="mx-auto max-w-6xl px-4 py-10 text-sm text-ink-soft">{site.name} · {site.tagline}</footer>
    </main>
  );
}
