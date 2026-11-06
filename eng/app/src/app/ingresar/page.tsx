import type { Metadata } from "next";
import { brand } from "@tramo/ui/brand";
import { app } from "@/content/app";
import { AuthFrame } from "@/components/app/auth-frame";
import { FreshPerLink, MagicLinkEntry } from "@/components/app/token-entry";

// The link carries its token in the fragment, which never reaches a server or a log, and the
// page sends no referrer, so nothing about the visit leaks to another site either.
export const metadata: Metadata = { title: `${app.session.magic.eyebrow} · ${brand.name}`, robots: { index: false }, referrer: "no-referrer" };

export default function Page() {
  return (
    <AuthFrame>
      <FreshPerLink>
        <MagicLinkEntry />
      </FreshPerLink>
    </AuthFrame>
  );
}
