import type { Metadata } from "next";
import { app } from "@/content/app";
import { Trays } from "@/components/app/views/trays";

export const metadata: Metadata = { title: app.trays.eyebrow };

const trays = ["suggested", "exception", "auto"] as const;
const one = (v: string | string[] | undefined) => (Array.isArray(v) ? v[0] : v);

export default async function Page({ searchParams }: { searchParams: Promise<Record<string, string | string[] | undefined>> }) {
  const asked = one((await searchParams).bandeja);
  return <Trays tray={trays.find((t) => t === asked) ?? "suggested"} />;
}
