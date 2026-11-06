import type { Metadata } from "next";
import { app } from "@/content/app";
import { Approvals } from "@/components/app/views/approvals";

export const metadata: Metadata = { title: app.approvals.eyebrow };

export default function Page() {
  return <Approvals />;
}
