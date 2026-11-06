import type { Metadata } from "next";
import { app } from "@/content/app";
import { Files } from "@/components/app/views/files";

export const metadata: Metadata = { title: app.files.eyebrow };

export default function Page() {
  return <Files />;
}
