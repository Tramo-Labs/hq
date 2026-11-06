import type { NextConfig } from "next";

/**
 * CORE_API_URL picks the mode at build time:
 * - unset: the portal and the console run on sample data with a visible banner.
 * - set (e.g. http://localhost:8080): /v1/* is rewritten to the core on the web's own origin,
 *   so the browser never talks to the core directly (no CORS) and the refresh cookie
 *   (SameSite=Strict, Path=/v1/auth) stays first party.
 * Rewrites are compiled into the build, so set it for `next build` and keep it for `next start`.
 * Only the mode reaches the browser bundle, never the URL.
 */
const core = process.env.CORE_API_URL?.trim().replace(/\/+$/, "");
if (core && !/^https?:\/\/[^/]+$/.test(core)) {
  throw new Error("CORE_API_URL must be an origin like http://localhost:8080, with no path");
}

const nextConfig: NextConfig = {
  env: { TRAMO_CORE_MODE: core ? "live" : "sample" },
  async rewrites() {
    return core ? [{ source: "/v1/:path*", destination: `${core}/v1/:path*` }] : [];
  },
};

export default nextConfig;
