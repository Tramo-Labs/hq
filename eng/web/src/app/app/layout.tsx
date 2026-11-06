import type { Metadata } from "next";
import { site } from "@/content/site";
import { app } from "@/content/app";
import { Shell } from "@/components/app/ui";
import { getSession } from "@/lib/api";

export const metadata: Metadata = {
  title: { template: `%s · ${app.portal.label} · ${site.name}`, default: `${app.portal.label} · ${site.name}` },
  robots: { index: false },
};

export default async function PortalLayout({ children }: { children: React.ReactNode }) {
  const session = await getSession("portal");
  return <Shell area="portal" session={session}>{children}</Shell>;
}
