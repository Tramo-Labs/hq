import type { Metadata } from "next";
import { site } from "@/content/site";
import { app } from "@/content/app";
import { Shell } from "@/components/app/ui";
import { getSession } from "@/lib/api";

export const metadata: Metadata = {
  title: { template: `%s · ${app.console.label} · ${site.name}`, default: `${app.console.label} · ${site.name}` },
  robots: { index: false },
};

export default async function ConsoleLayout({ children }: { children: React.ReactNode }) {
  const session = await getSession("console");
  return <Shell area="console" session={session}>{children}</Shell>;
}
