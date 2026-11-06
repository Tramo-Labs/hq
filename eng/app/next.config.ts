import type { NextConfig } from "next";
import { PHASE_DEVELOPMENT_SERVER } from "next/constants";
import path from "node:path";

/** eng/: next dev and next build run from eng/app, locally, in CI and on Vercel. */
const eng = path.resolve(process.cwd(), "..");

/**
 * The portal (/app), the console (/operador) and the pages before a session (/entrar, /ingresar,
 * /invitacion). The landing is a separate app, eng/web (D-21).
 *
 * Where the data comes from, D-18:
 * - NEXT_PUBLIC_API_BASE (production): the browser calls the core's API origin directly,
 *   with CORS and credentials 'include' for the refresh cookie. App and API share the
 *   registrable domain (app.X and api.X), so the SameSite=Strict cookie travels. https only;
 *   plain http is accepted for loopback hosts, to try the build on one machine.
 * - CORE_API_URL (local development): /v1/* is rewritten to the core on the app's own origin.
 *   `next dev` takes any origin; a build takes it only for a loopback host, never as the way
 *   production reaches the core.
 * - neither: sample data with a visible banner.
 * Both are compiled into the build, so set them for `next build` and keep them for `next start`.
 */
const origin = /^https?:\/\/[^/?#]+$/;
const loopback = (url: string) => ["localhost", "127.0.0.1", "[::1]"].includes(new URL(url).hostname);
const clean = (v: string | undefined) => v?.trim().replace(/\/+$/, "") || undefined;

export default function config(phase: string): NextConfig {
  const dev = phase === PHASE_DEVELOPMENT_SERVER;
  const apiBase = clean(process.env.NEXT_PUBLIC_API_BASE);
  const core = clean(process.env.CORE_API_URL);
  if (apiBase && core) throw new Error("Set NEXT_PUBLIC_API_BASE (direct, production) or CORE_API_URL (local proxy), not both");
  if (apiBase) {
    if (!origin.test(apiBase)) throw new Error("NEXT_PUBLIC_API_BASE must be an origin like https://api.example.com, with no path");
    if (!apiBase.startsWith("https://") && !loopback(apiBase)) throw new Error("NEXT_PUBLIC_API_BASE must use https (http only for a loopback host)");
  }
  if (core) {
    if (!origin.test(core)) throw new Error("CORE_API_URL must be an origin like http://localhost:8080, with no path");
    if (!dev && !loopback(core)) throw new Error("CORE_API_URL is the local development proxy; a production build reaches the core with NEXT_PUBLIC_API_BASE");
  }

  return {
    // eng/ is the root and @tramo/ui is compiled here: the shared package lives in eng/ui (D-21).
    turbopack: { root: eng },
    outputFileTracingRoot: eng,
    transpilePackages: ["@tramo/ui"],
    // Only the mode and the public API origin reach the browser; CORE_API_URL never does.
    env: { TRAMO_CORE_MODE: apiBase || core ? "live" : "sample", TRAMO_API_BASE: apiBase ?? "" },
    async rewrites() {
      return core ? [{ source: "/v1/:path*", destination: `${core}/v1/:path*` }] : [];
    },
  };
}
