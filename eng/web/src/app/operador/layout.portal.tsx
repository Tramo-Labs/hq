import type { Metadata } from "next";
import { site } from "@/content/site";
import { app } from "@/content/app";
import { Shell } from "@/components/app/shell";

export const metadata: Metadata = {
  title: { template: `%s · ${app.console.label} · ${site.name}`, default: `${app.console.label} · ${site.name}` },
  robots: { index: false },
};

export default function Layout({ children }: { children: React.ReactNode }) {
  return <Shell area="console">{children}</Shell>;
}
