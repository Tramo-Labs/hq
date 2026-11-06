import type { NextConfig } from "next";
import path from "node:path";

/**
 * The landing, alone (D-21): the portal and the console are eng/app. It reads no environment
 * variable and talks to no API.
 *
 * eng/ is the root because the shared tokens live in eng/ui, outside this app. next dev and
 * next build run from eng/web, locally, in CI and on Vercel.
 */
const eng = path.resolve(process.cwd(), "..");

const config: NextConfig = {
  turbopack: { root: eng },
  outputFileTracingRoot: eng,
};

export default config;
