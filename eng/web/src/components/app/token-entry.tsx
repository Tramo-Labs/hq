"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useRef, useState } from "react";
import { app } from "@/content/app";
import { acceptMagicLink, ApiError, homeFor } from "@/lib/api";
import { ghostBtn } from "@/components/app/buttons";

/**
 * The token of an emailed link travels in the fragment (#token=…), which the browser never
 * sends to any server. It is read once and wiped from the address bar and history before
 * anything else, so it does not linger in the URL, in a screenshot or in a later share.
 */
export function readFragmentToken() {
  const token = new URLSearchParams(window.location.hash.slice(1)).get("token");
  if (window.location.hash) window.history.replaceState(null, "", window.location.pathname);
  return token;
}

/** Errors shared by the pages that take an emailed token. */
export function explainToken(e: unknown, invalid: string) {
  const s = app.session;
  if (e instanceof ApiError) {
    if (e.status === 401) return invalid;
    if (e.code === "not_a_member") return s.magic.notMember;
    if (e.status === 429) return e.retryAfter ? s.rateLimited(e.retryAfter) : s.rateLimitedNoWait;
    if (e.status >= 500) return s.offline;
  }
  return e instanceof TypeError ? s.offline : s.failed;
}

/** /ingresar: opens the session from a magic link, then goes to the person's home. */
export function MagicLinkEntry() {
  const m = app.session.magic;
  const router = useRouter();
  const [error, setError] = useState<string | null>(null);
  // The token is single use and wiped on read: one request per page, kept across React's
  // double mount in development (refs survive it), each mount only listening to the answer.
  const started = useRef<Promise<string | null> | null>(null);

  useEffect(() => {
    let live = true;
    started.current ??= (() => {
      const token = readFragmentToken();
      if (!token) return Promise.resolve(m.noToken);
      return acceptMagicLink(token).then((s) => { router.replace(homeFor(s.role)); return null; }, (e) => explainToken(e, m.invalid));
    })();
    started.current.then((msg) => { if (live && msg) setError(msg); });
    return () => { live = false; };
  }, [m, router]);

  return (
    <>
      <p className="font-mono text-[12px] tracking-wide text-white/60">{m.eyebrow}</p>
      {error ? (
        <>
          <p role="alert" className="mt-4 text-[16px] leading-relaxed text-accent">{error}</p>
          <Link href="/entrar" className={`${ghostBtn} mt-6`}>{m.again}</Link>
        </>
      ) : (
        <p role="status" className="mt-4 text-[16px] text-white/80">{m.opening}</p>
      )}
    </>
  );
}
