import { asOf, currentTenantId, files, matches, metrics, operatorSession, session, tenants } from "./fixtures";
import type { Match, Motive, Outcome, Page, Role, Tenant, Tray } from "./types";

export type * from "./types";

/**
 * The only door the screens use to reach data. Today it answers from fixtures; when the
 * core serves the paths below, each function becomes a fetch against the same path and
 * nothing in the screens changes. Lists come as Page<T>, errors as problem+json.
 *
 *   GET  /v1/me                            session: name and role in the active tenant
 *   GET  /v1/me/tenant                     client: own tenant and onboarding
 *   GET  /v1/tenants                       operator: clients and missing steps
 *   GET  /v1/matches?tray=&tenant=&motive= trays
 *   POST /v1/matches/{id}/decision         Idempotency-Key required
 *   GET  /v1/metrics?tenant=&period=       panel, each number with its source
 *   GET  /v1/files                         uploaded statements and documents
 *   POST /v1/exports                       full download, built in the background
 */

export const isSample = true;
export const today = asOf;

const page = <T,>(items: T[]): Page<T> => ({ items });

export async function getSession(area: "portal" | "console") {
  return area === "portal" ? session : operatorSession;
}

/** What each role may do, from the table in product/ESPECIFICACION.md section 1. */
export function can(role: Role, action: "decideException" | "decideSuggested" | "download") {
  if (action === "decideException") return role === "owner" || role === "approver";
  if (action === "decideSuggested") return role === "operator";
  return role === "owner" || role === "accountant" || role === "operator";
}

export async function getCurrentTenant() {
  return tenants.find((t) => t.id === currentTenantId)!;
}

export function missingSteps(t: Tenant) {
  return [t.arca, t.bank, t.erp].filter((s) => s.status !== "ready").length;
}

export async function listTenants() {
  return page([...tenants].sort((a, b) => missingSteps(b) - missingSteps(a) || b.openExceptions - a.openExceptions));
}

export async function listMatches(filter: { tray?: Tray; tenantId?: string; motive?: Motive } = {}) {
  return page<Match>(
    matches
      .filter((m) => (!filter.tray || m.tray === filter.tray) && (!filter.tenantId || m.tenantId === filter.tenantId) && (!filter.motive || m.motive === filter.motive))
      .filter((m) => !m.decision)
      .sort((a, b) => a.openedAt.localeCompare(b.openedAt)),
  );
}

const applied = new Map<string, { by: string; at: string; outcome: Outcome; note?: string }>();

/**
 * POST /v1/matches/{id}/decision. The same key returns the first answer, so a double tap
 * applies the decision once (spec section 4).
 */
export async function decide(id: string, body: { outcome: Outcome; note?: string }, idempotencyKey: string, by: string) {
  if (!idempotencyKey) throw new Error("Idempotency-Key is required");
  const prior = applied.get(idempotencyKey);
  if (prior) return { id, decision: prior };
  const decision = { by, at: new Date().toISOString(), ...body };
  applied.set(idempotencyKey, decision);
  return { id, decision };
}

export async function listMetrics() {
  return page(metrics);
}

export async function listFiles() {
  return page(files);
}

/** POST /v1/exports: the core builds a ZIP and mails a link that expires in 24 hours. */
export async function requestFullExport(idempotencyKey: string) {
  if (!idempotencyKey) throw new Error("Idempotency-Key is required");
  return { status: "queued" as const, expiresInHours: 24 };
}

export function daysSince(date: string) {
  return Math.round((Date.parse(today) - Date.parse(date)) / 86_400_000);
}
