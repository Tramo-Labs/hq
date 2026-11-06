import { asOf, currentTenantId, files, matches, operatorSession, session, tenants } from "./fixtures";
import type { Match, Metric, MetricList, Motive, Outcome, Page, Role, Tenant, Tray } from "./types";

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
 *   GET  /v1/metrics?tenant=&period=       panel, each number with its source and list
 *   GET  /v1/files                         uploaded statements and documents
 *   POST /v1/exports                       full download (not wired: the action stays disabled)
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

/**
 * GET /v1/metrics. Every number is computed from the sample matches, never typed in, so
 * the panel only shows what the data says. What the samples cannot measure (balances,
 * documents) comes back as null and reads "no data yet". A tenant with no matches has
 * no numbers at all.
 */
export async function listMetrics(tenantId: string) {
  const tenant = tenants.find((t) => t.id === tenantId);
  const own = matches.filter((m) => m.tenantId === tenantId);
  if (!tenant || own.length === 0) return page<Metric>([]);
  const open = own.filter((m) => m.tray === "exception" && !m.decision);
  const held = open.filter((m) => m.motive === "over_threshold");
  const auto = own.filter((m) => m.tray === "auto");
  const caught = own.filter((m) => m.motive === "duplicate" && m.decision?.outcome === "rejected");
  const exceptions: MetricList = { tray: "exception" };
  const items: Metric[] = [
    { key: "auto_rate", value: Math.round((100 * auto.length) / own.length), unit: "percent", period: "Hoy",
      source: `${auto.length} ${auto.length === 1 ? "match" : "matches"} en Automático ÷ ${own.length} ${own.length === 1 ? "movimiento" : "movimientos"} del cliente`, list: { tray: "auto" } },
    { key: "open_exceptions", value: open.length, unit: "count", period: "Hoy", source: "matches en Excepciones sin decisión", list: exceptions },
    ...(open.length > 0
      ? [{ key: "oldest_exception", value: Math.max(...open.map((m) => daysSince(m.openedAt))), unit: "days", period: "Hoy",
          source: "días desde la apertura de la excepción abierta más vieja", list: exceptions } satisfies Metric]
      : []),
    { key: "held_invoices", value: held.length, unit: "count", period: "Hoy", source: "facturas en Excepciones por monto sobre el umbral, sin decidir",
      list: { tray: "exception", motive: "over_threshold" } },
    { key: "duplicates", value: caught.length, unit: "count", period: "Hoy", source: "excepciones por posible duplicado que el cliente rechazó",
      list: { tray: "exception", motive: "duplicate" } },
    { key: "bank_vs_books", value: null, unit: "ars", period: "Hoy", source: "saldo del extracto menos saldo contable, por cuenta; requiere el ledger y el sistema contable",
      byAccount: tenant.bank.accounts.map((a) => ({ account: `${a.bank} ****${a.last4}`, value: null })) },
    { key: "invoices", value: null, unit: "count", period: "Hoy", source: "documentos leídos; requiere el módulo de documentos" },
  ];
  return page(items);
}

/**
 * Turns the list behind a number into a link for each area. The client only has its
 * exceptions inbox, so a number whose list lives in another tray has no portal link and
 * stays off the client panel.
 */
export function metricHref(list: MetricList | undefined, area: "portal" | "console", tenantId: string) {
  if (!list) return undefined;
  if (area === "portal") {
    if (list.tray !== "exception") return undefined;
    return list.motive ? `/app/aprobaciones?motivo=${list.motive}` : "/app/aprobaciones";
  }
  const q = new URLSearchParams({ bandeja: list.tray, cliente: tenantId });
  if (list.motive) q.set("motivo", list.motive);
  return `/operador/bandejas?${q}`;
}

export async function listFiles() {
  return page(files);
}

/**
 * POST /v1/exports. Placeholder for the contract only: nothing calls it yet, and the portal
 * shows the download disabled until the core builds and delivers the export.
 */
export async function requestFullExport(idempotencyKey: string) {
  if (!idempotencyKey) throw new Error("Idempotency-Key is required");
  return { status: "queued" as const, expiresInHours: 24 };
}

export function daysSince(date: string) {
  return Math.round((Date.parse(today) - Date.parse(date)) / 86_400_000);
}
