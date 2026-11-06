import type { NextConfig } from "next";
import { PHASE_DEVELOPMENT_SERVER } from "next/constants";

/**
 * PORTAL_ENABLED decides whether the portal (/app), the console (/operador) and the pages
 * before a session (/entrar, /ingresar, /invitacion) exist at all. Their route files are named
 * *.portal.tsx and only count as routes when it is on, so with it off they are not compiled:
 * 404, not in the bundle, no links.
 * - `next build`: off unless PORTAL_ENABLED=1. Production ships the landing alone by default.
 * - `next dev`: on unless PORTAL_ENABLED=0.
 *
 * Where the portal's data comes from (only with the portal on), D-18:
 * - NEXT_PUBLIC_API_BASE (production): the browser calls the core's API origin directly,
 *   with CORS and credentials 'include' for the refresh cookie. Web and API share the
 *   registrable domain (app.X and api.X), so the SameSite=Strict cookie travels. https only;
 *   plain http is accepted for loopback hosts, to try the build on one machine.
 * - CORE_API_URL (local development): /v1/* is rewritten to the core on the web's own origin.
 *   `next dev` takes any origin; a build takes it only for a loopback host, never as the way
 *   production reaches the core.
 * - neither: sample data with a visible banner.
 * Both are compiled into the build, so set them for `next build` and keep them for `next start`.
 */
const origin = /^https?:\/\/[^/?#]+$/;
const loopback = (url: string) => ["localhost", "127.0.0.1", "[::1]"].includes(new URL(url).hostname);
const clean = (v: string | undefined) => v?.trim().replace(/\/+$/, "") || undefined;

export default function config(phase: string): NextConfig {
  const flag = process.env.PORTAL_ENABLED?.trim();
  if (flag !== undefined && flag !== "" && flag !== "0" && flag !== "1") {
    throw new Error("PORTAL_ENABLED must be 1 (portal on) or 0 (landing only)");
  }
  const dev = phase === PHASE_DEVELOPMENT_SERVER;
  const portal = flag === "1" || ((flag === undefined || flag === "") && dev);

  const apiBase = portal ? clean(process.env.NEXT_PUBLIC_API_BASE) : undefined;
  const core = portal ? clean(process.env.CORE_API_URL) : undefined;
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
    pageExtensions: portal ? ["portal.tsx", "tsx", "ts"] : ["tsx", "ts"],
    // Only the mode and the public API origin reach the browser; CORE_API_URL never does.
    env: { TRAMO_CORE_MODE: apiBase || core ? "live" : "sample", TRAMO_API_BASE: apiBase ?? "" },
    async rewrites() {
      return core ? [{ source: "/v1/:path*", destination: `${core}/v1/:path*` }] : [];
    },
  };
}
