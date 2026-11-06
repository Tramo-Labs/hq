import type { Metadata } from "next";
import { DM_Sans, Geist_Mono } from "next/font/google";
import "./globals.css";
import { brand } from "@tramo/ui/brand";

// The same fonts as the landing; next/font only works called from the app's own code, so each
// app loads them into the variables eng/ui/tokens.css uses.
const sans = DM_Sans({ subsets: ["latin"], variable: "--font-dm-sans" });
const mono = Geist_Mono({ subsets: ["latin"], variable: "--font-geist-mono" });

// Nothing in the app is for search engines; each area sets its own title.
export const metadata: Metadata = { title: brand.name, robots: { index: false } };

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="es-AR" className={`${sans.variable} ${mono.variable}`}>
      <body className="antialiased">{children}</body>
    </html>
  );
}
