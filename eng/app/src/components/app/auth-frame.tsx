import type { ReactNode } from "react";
import Link from "next/link";
import { brand } from "@tramo/ui/brand";
import { app } from "@/content/app";
import { getMode } from "@/lib/api";

/** The frame of the pages before a session: /entrar, /ingresar and /invitacion. */
export function AuthFrame({ children }: { children: ReactNode }) {
  return (
    <div className="min-h-dvh">
      <header className="border-b border-white/10">
        <div className="mx-auto flex max-w-5xl items-center px-4 py-3">
          <Link href="/" className="inline-flex min-h-11 items-center text-[17px] font-medium tracking-tight">{brand.name}</Link>
        </div>
      </header>
      {getMode() === "sample" && <p className="border-b border-accent/30 bg-accent/10 px-4 py-2 text-center font-mono text-[12px] text-accent">{app.sample}</p>}
      <main id="contenido" className="mx-auto max-w-md px-4 pb-24 pt-10 sm:pt-16">{children}</main>
    </div>
  );
}
