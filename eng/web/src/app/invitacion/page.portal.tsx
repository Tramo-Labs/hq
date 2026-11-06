import type { Metadata } from "next";
import { site } from "@/content/site";
import { app } from "@/content/app";
import { AuthFrame } from "@/components/app/auth-frame";
import { InviteEntry } from "@/components/app/token-entry";

// Same as /ingresar: the token is in the fragment and the page sends no referrer.
export const metadata: Metadata = { title: `${app.session.invite.eyebrow} · ${site.name}`, robots: { index: false }, referrer: "no-referrer" };

export default function Page() {
  return (
    <AuthFrame>
      <InviteEntry />
    </AuthFrame>
  );
}
