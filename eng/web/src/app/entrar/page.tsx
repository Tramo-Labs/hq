import type { Metadata } from "next";
import Link from "next/link";
import { site } from "@/content/site";
import { app } from "@/content/app";
import { Login } from "@/components/app/login";

export const metadata: Metadata = { title: `${app.session.eyebrow} · ${site.name}`, robots: { index: false } };

const one = (v: string | string[] | undefined) => (Array.isArray(v) ? v[0] : v);

export default async function Page({ searchParams }: { searchParams: Promise<Record<string, string | string[] | undefined>> }) {
  const sp = await searchParams;
  return (
    <div className="min-h-dvh">
      <header className="border-b border-white/10">
        <div className="mx-auto flex max-w-5xl items-center px-4 py-3">
          <Link href="/" className="inline-flex min-h-11 items-center text-[17px] font-medium tracking-tight">{site.name}</Link>
        </div>
      </header>
      <main id="contenido" className="mx-auto max-w-md px-4 pb-24 pt-10 sm:pt-16">
        <Login back={one(sp.volver)} switching={one(sp.cambiar) === "1"} tenantId={one(sp.empresa)} expired={one(sp.motivo) === "vencida"} />
      </main>
    </div>
  );
}
