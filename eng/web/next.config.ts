import type { NextConfig } from "next";
import { PHASE_DEVELOPMENT_SERVER } from "next/constants";

/**
 * PORTAL_ENABLED decides whether the portal (/app), the console (/operador) and the login
 * (/entrar) exist at all. Their route files are named *.portal.tsx and only count as routes
 * when it is on, so with it off they are not compiled: 404, not in the bundle, no links.
 * - `next build`: off unless PORTAL_ENABLED=1. Production ships the landing alone by default.
 * - `next dev`: on unless PORTAL_ENABLED=0.
 *
 * CORE_API_URL picks the portal's data at build time (only with the portal on):
 * - unset: sample data with a visible banner.
 * - set (e.g. http://localhost:8080): /v1/* is rewritten to the core on the web's own origin,
 *   so the browser never talks to the core directly (no CORS) and the refresh cookie
 *   (SameSite=Strict, Path=/v1/auth) stays first party.
 * Both are compiled into the build, so set them for `next build` and keep them for `next start`.
 * Only the mode reaches the browser bundle, never the URL.
 */
export default function config(phase: string): NextConfig {
  const flag = process.env.PORTAL_ENABLED?.trim();
  if (flag !== undefined && flag !== "" && flag !== "0" && flag !== "1") {
    throw new Error("PORTAL_ENABLED must be 1 (portal on) or 0 (landing only)");
  }
  const portal = flag === "1" || ((flag === undefined || flag === "") && phase === PHASE_DEVELOPMENT_SERVER);

  const core = portal ? process.env.CORE_API_URL?.trim().replace(/\/+$/, "") : undefined;
  if (core && !/^https?:\/\/[^/]+$/.test(core)) {
    throw new Error("CORE_API_URL must be an origin like http://localhost:8080, with no path");
  }

  return {
    pageExtensions: portal ? ["portal.tsx", "tsx", "ts"] : ["tsx", "ts"],
    env: { TRAMO_CORE_MODE: core ? "live" : "sample" },
    async rewrites() {
      return core ? [{ source: "/v1/:path*", destination: `${core}/v1/:path*` }] : [];
    },
  };
}
