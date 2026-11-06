import type { Metadata } from "next";
import { brand } from "@tramo/ui/brand";
import { app } from "@/content/app";
import { AuthFrame } from "@/components/app/auth-frame";
import { FreshPerLink, InviteEntry } from "@/components/app/token-entry";

// Same as /ingresar: the token is in the fragment and the page sends no referrer.
export const metadata: Metadata = { title: `${app.session.invite.eyebrow} · ${brand.name}`, robots: { index: false }, referrer: "no-referrer" };

export default function Page() {
  return (
    <AuthFrame>
      <FreshPerLink>
        <InviteEntry />
      </FreshPerLink>
    </AuthFrame>
  );
}
