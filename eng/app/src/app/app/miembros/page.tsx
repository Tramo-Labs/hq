import type { Metadata } from "next";
import { app } from "@/content/app";
import { Members } from "@/components/app/views/members";

export const metadata: Metadata = { title: app.members.eyebrow };

export default function Page() {
  return <Members />;
}
