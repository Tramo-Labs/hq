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
  /** From /v1/me; null until the person sets one (the header shows the email then). */
  name: string | null;
  memberships: Me["memberships"];
};

export type Mode = "sample" | "live";

/** Inlined at build by next.config.ts: live with NEXT_PUBLIC_API_BASE or CORE_API_URL, else sample. */
const mode: Mode = process.env.TRAMO_CORE_MODE === "live" ? "live" : "sample";
let current: Session | null = null;
let endedByPeer = false;
const listeners = new Set<() => void>();

export function getMode() {
  return mode;
}

export function getSession() {
  return current;
}

/** True when this tab's session was dropped because another tab signed in as someone else or out. */
export function endedElsewhere() {
  return endedByPeer;
}

/**
 * F33: every tab of a browser shares one refresh cookie, so a login, an accepted invitation or a
 * logout in one tab changes who the others are on their next refresh. A tab holding a session
 * drops it as soon as another tab announces a different person (or nobody), instead of showing
 * the previous person's name and role until it reloads. Same person: nothing changes.
 */
export function peerEnds(mine: Pick<Session, "email"> | null, announced: string | null) {
  return mine !== null && announced !== mine.email;
}

// Live only: in sample mode each tab has its own made-up session and there is no shared cookie.
// Only the email travels between tabs, never a token.
let channel: BroadcastChannel | null | undefined;
function tabs() {
  if (channel !== undefined) return channel;
  channel = mode === "live" && typeof window !== "undefined" && typeof BroadcastChannel !== "undefined" ? new BroadcastChannel("tramo-session") : null;
  channel?.addEventListener("message", (e: MessageEvent) => {
    const announced = (e.data as { email?: unknown } | null)?.email;
    if (announced !== null && typeof announced !== "string") return;
    if (!peerEnds(current, announced)) return;
    current = null;
    endedByPeer = true;
    listeners.forEach((l) => l());
  });
  return channel;
}

export function setSession(s: Session | null) {
  const before = current?.email ?? null;
  current = s;
  endedByPeer = false;
  listeners.forEach((l) => l());
  const after = s?.email ?? null;
  if (after !== before) tabs()?.postMessage({ email: after });
}

export function subscribe(l: () => void) {
  listeners.add(l);
  return () => {
    listeners.delete(l);
  };
}
