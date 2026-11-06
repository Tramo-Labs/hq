import { ApiError, SessionExpired } from "./errors";
import { getSession, setSession, type Session } from "./session";
import type { InvitePreview, Me, Tokens } from "./types";

/**
 * The real core. In production (D-18) the browser calls the API origin directly
 * (NEXT_PUBLIC_API_BASE, inlined at build as TRAMO_API_BASE) with CORS; in local development
 * it goes through the web's own /v1 rewrite. Either way credentials are 'include', so the
 * refresh cookie (HttpOnly, SameSite=Strict, Path=/v1/auth) travels on refresh, switch-tenant
 * and logout. The access token goes in the Authorization header; no API secret is in the page.
 */

const json = { "Content-Type": "application/json" };
const base = process.env.TRAMO_API_BASE ?? "";

async function call(path: string, init: RequestInit = {}) {
  return fetch(base + path, { ...init, credentials: "include", cache: "no-store" });
}

async function tokens(res: Response): Promise<Tokens> {
  if (!res.ok) throw await ApiError.from(res);
  return (await res.json()) as Tokens;
}

async function fetchMe(accessToken: string): Promise<Me> {
  const res = await call("/v1/me", { headers: { Authorization: `Bearer ${accessToken}` } });
  if (!res.ok) throw await ApiError.from(res);
  return (await res.json()) as Me;
}

async function open(t: Tokens): Promise<Session> {
  const me = await fetchMe(t.accessToken);
  const s: Session = {
    accessToken: t.accessToken,
    tenantId: t.tenantId,
    role: t.role,
    email: me.email,
    name: me.name,
    memberships: me.memberships,
    tenantName: me.memberships.find((m) => m.tenantId === t.tenantId)?.tenantName ?? "",
  };
  setSession(s);
  return s;
}

export async function login(email: string, password: string, tenantId?: string) {
  const res = await call("/v1/auth/login", {
    method: "POST",
    headers: json,
    body: JSON.stringify({ email, password, tenantId, tokenDelivery: "cookie" }),
  });
  return open(await tokens(res));
}

/** Asks for a one time login link by email. The core always answers 202, account or not. */
export async function requestMagicLink(email: string, idempotencyKey: string, tenantId?: string) {
  const res = await call("/v1/auth/magic-link", {
    method: "POST",
    headers: { ...json, "Idempotency-Key": idempotencyKey },
    body: JSON.stringify({ email, tenantId }),
  });
  if (!res.ok) throw await ApiError.from(res);
}

/** Opens a session with the token from the emailed link (/ingresar#token=…). */
export async function acceptMagicLink(token: string) {
  const res = await call("/v1/auth/magic-link/accept", { method: "POST", headers: json, body: JSON.stringify({ token, tokenDelivery: "cookie" }) });
  return open(await tokens(res));
}

/**
 * Accepts the invitation with the token from the email (/invitacion#token=…) and opens a session.
 * An existing account sends no password (D-19); a person without one sends password and name.
 */
export async function acceptInvite(token: string, password?: string, name?: string) {
  const res = await call("/v1/auth/invite/accept", {
    method: "POST",
    headers: json,
    body: JSON.stringify({ token, password, name, tokenDelivery: "cookie" }),
  });
  return open(await tokens(res));
}

/**
 * What an invitation is for, before accepting it (F38): the company and the role. Anonymous, the
 * token in the body (never in the URL); it does not spend the token. Same errors as accept.
 */
export async function previewInvite(token: string): Promise<InvitePreview> {
  const res = await call("/v1/auth/invite/preview", { method: "POST", headers: json, body: JSON.stringify({ token }) });
  if (!res.ok) throw await ApiError.from(res);
  return (await res.json()) as InvitePreview;
}

let refreshing: Promise<Session> | null = null;

/**
 * Trades the HttpOnly cookie for a new access token. One flight at a time: the refresh
 * token rotates on every use, and a second use of the old one revokes the whole family.
 */
export function refresh() {
  refreshing ??= (async () => {
    try {
      const res = await call("/v1/auth/refresh", { method: "POST", headers: json, body: JSON.stringify({ tokenDelivery: "cookie" }) });
      return await open(await tokens(res));
    } finally {
      refreshing = null;
    }
  })();
  return refreshing;
}

/**
 * Moves the session to another of the person's tenants, no password: the refresh cookie
 * authorizes it and rotates. It waits for any refresh in flight and holds the same slot, since
 * two uses of one refresh token would revoke the whole family.
 */
export async function switchTenant(tenantId: string) {
  if (refreshing) await refreshing.catch(() => undefined);
  const run = (async () => {
    try {
      const res = await call("/v1/auth/switch-tenant", { method: "POST", headers: json, body: JSON.stringify({ tenantId, tokenDelivery: "cookie" }) });
      return await open(await tokens(res));
    } finally {
      refreshing = null;
    }
  })();
  refreshing = run;
  return run;
}

export async function logout() {
  try {
    await call("/v1/auth/logout", { method: "POST" });
  } finally {
    setSession(null);
  }
}

/**
 * An authenticated request. On 401 it refreshes once and retries; if that fails too, the
 * session is cleared and the caller gets SessionExpired (the provider sends it to /entrar).
 */
export async function request(path: string, init: RequestInit = {}): Promise<Response> {
  const attempt = (s: Session | null) =>
    call(path, { ...init, headers: { ...(init.headers as Record<string, string>), ...(s ? { Authorization: `Bearer ${s.accessToken}` } : {}) } });

  let res = await attempt(getSession());
  if (res.status === 401) {
    try {
      res = await attempt(await refresh());
    } catch {
      setSession(null);
      throw new SessionExpired();
    }
    if (res.status === 401) {
      setSession(null);
      throw new SessionExpired();
    }
  }
  if (!res.ok) throw await ApiError.from(res);
  return res;
}

/** PATCH /v1/me: the person's own name; the session in memory takes it at once. */
export async function updateName(name: string) {
  const res = await request("/v1/me", { method: "PATCH", headers: json, body: JSON.stringify({ name }) });
  const me = (await res.json()) as Me;
  const s = getSession();
  if (s) setSession({ ...s, name: me.name });
  return me.name;
}

export async function getJson<T>(path: string): Promise<T> {
  return (await (await request(path)).json()) as T;
}

export async function postJson<T>(path: string, body: unknown, idempotencyKey: string): Promise<T> {
  const res = await request(path, { method: "POST", headers: { ...json, "Idempotency-Key": idempotencyKey }, body: JSON.stringify(body) });
  return (await res.json()) as T;
}
