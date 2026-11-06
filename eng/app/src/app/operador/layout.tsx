import type { Metadata } from "next";
import { brand } from "@tramo/ui/brand";
import { app } from "@/content/app";
import { Shell } from "@/components/app/shell";

export const metadata: Metadata = {
  title: { template: `%s · ${app.console.label} · ${brand.name}`, default: `${app.console.label} · ${brand.name}` },
  robots: { index: false },
};

export default function Layout({ children }: { children: React.ReactNode }) {
  return <Shell area="console">{children}</Shell>;
}
