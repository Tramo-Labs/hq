import type { Metadata } from "next";
import { site } from "@/content/site";
import { app } from "@/content/app";
import { Login } from "@/components/app/login";
import { AuthFrame } from "@/components/app/auth-frame";

export const metadata: Metadata = { title: `${app.session.eyebrow} · ${site.name}`, robots: { index: false } };

const one = (v: string | string[] | undefined) => (Array.isArray(v) ? v[0] : v);

export default async function Page({ searchParams }: { searchParams: Promise<Record<string, string | string[] | undefined>> }) {
  const sp = await searchParams;
  return (
    <AuthFrame>
      <Login back={one(sp.volver)} switching={one(sp.cambiar) === "1"} tenantId={one(sp.empresa)} expired={one(sp.motivo) === "vencida"} />
    </AuthFrame>
  );
}
