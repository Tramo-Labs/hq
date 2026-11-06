import type { Me, Role } from "./types";

/**
 * The session lives in this module's memory and nowhere else: no localStorage, no
 * sessionStorage, no readable cookie. A reload loses it on purpose; the provider then asks
 * the core for a new access token with the HttpOnly refresh cookie, which JavaScript never sees.
 */
export type Session = {
  /** Empty in sample mode. */
  accessToken: string;
  tenantId: string;
  tenantName: string;
  role: Role;
  email: string;
  memberships: Me["memberships"];
};

export type Mode = "sample" | "live";

/** Inlined at build by next.config.ts from CORE_API_URL; the URL itself never reaches the browser. */
const mode: Mode = process.env.TRAMO_CORE_MODE === "live" ? "live" : "sample";
let current: Session | null = null;
const listeners = new Set<() => void>();

export function getMode() {
  return mode;
}

export function getSession() {
  return current;
}

export function setSession(s: Session | null) {
  current = s;
  listeners.forEach((l) => l());
}

export function subscribe(l: () => void) {
  listeners.add(l);
  return () => {
    listeners.delete(l);
  };
}
