import type { Metadata } from "next";
import { app } from "@/content/app";
import { Results } from "@/components/app/views/results";

export const metadata: Metadata = { title: app.results.eyebrow };

const one = (v: string | string[] | undefined) => (Array.isArray(v) ? v[0] : v);

export default async function Page({ searchParams }: { searchParams: Promise<Record<string, string | string[] | undefined>> }) {
  return <Results area="console" period={one((await searchParams).periodo)} />;
}
