import { ApiError } from "./errors";
import * as live from "./live";
import { getMode, getSession, setSession, type Session } from "./session";
import type {
  Decision, DecisionRequest, ExportInfo, ExportKind, Match, MatchTray, Metric, MovementDecision, MovementDecisionRequest,
  Page, PendingMovement, PendingTray, QuarantineList, Account, Role, TenantStatus, ItemDirection, OpenItems, Member, InvitePreview,
} from "./types";

export type * from "./types";
export { ApiError, SessionExpired } from "./errors";
export { endedElsewhere, getMode, getSession, subscribe, type Mode, type Session } from "./session";

/**
 * The only door the screens use to reach data. Two modes, chosen at build (next.config.ts):
 *
 * - live: fetches to the core's API origin (NEXT_PUBLIC_API_BASE, production, D-18) or to the
 *   web's own /v1 rewrite (CORE_API_URL, local development), credentials 'include'. Access
 *   token in memory, refresh token in the core's HttpOnly cookie.
 * - sample: the fixtures, in the same shapes, loaded on demand so live mode never ships them.
 *
 * Paths used (eng/core/api/openapi.yaml):
 *   POST /v1/auth/login | refresh | logout | switch-tenant
 *   POST /v1/auth/magic-link | magic-link/accept | invite/preview | invite/accept
 *   GET  /v1/me, PATCH /v1/me                    GET /v1/me/tenant
 *   GET  /v1/tenants (operator, accountant)      GET /v1/ledger/accounts, /v1/ledger/files?status=quarantined
 *   GET  /v1/matching/matches?tray=&period=      GET /v1/matching/pending?tray=&period=
 *   GET  /v1/matching/items?direction=&period=
 *   POST /v1/matching/matches/{id}/decision      POST /v1/matching/movements/{id}/decision
 *   GET  /v1/metrics?period=                     GET /v1/exports, GET /v1/exports/{kind}
 *   GET, POST /v1/tenant/members                 DELETE /v1/tenant/members/{userId}
 * Every POST except auth carries an Idempotency-Key the caller created for that user action.
 */

const sample = () => import("./sample");
const isLive = () => getMode() === "live";
const q = (params: Record<string, string | undefined>) => {
  const s = new URLSearchParams(Object.entries(params).filter((e): e is [string, string] => !!e[1])).toString();
  return s ? `?${s}` : "";
};

// session

export async function login(email: string, password: string, tenantId?: string) {
  return isLive() ? live.login(email, password, tenantId) : (await sample()).login(email, password, tenantId);
}

/** POST /v1/auth/magic-link: always 202, so it never tells whether the email has an account. */
export async function requestMagicLink(email: string, idempotencyKey: string, tenantId?: string) {
  return isLive() ? live.requestMagicLink(email, idempotencyKey, tenantId) : (await sample()).requestMagicLink();
}

/** POST /v1/auth/magic-link/accept with the token from the link, and the session it opens. */
export async function acceptMagicLink(token: string) {
  return isLive() ? live.acceptMagicLink(token) : (await sample()).acceptMagicLink();
}

/** POST /v1/auth/invite/accept with the session's token (F41): 204 joins that account, 409 invite_for_another_account. */
export async function acceptInviteAs(token: string): Promise<void> {
  return isLive() ? live.acceptInviteAs(token) : (await sample()).acceptInviteAs();
}

/** GET /v1/me into the session, after joining a company (F41). */
export async function reloadMe(): Promise<Session> {
  return isLive() ? live.reloadMe() : (await sample()).reloadMe();
}

/** POST /v1/auth/invite/preview: the company and role of an invitation, before accepting (F38). */
export async function previewInvite(token: string): Promise<InvitePreview> {
  return isLive() ? live.previewInvite(token) : (await sample()).previewInvite();
}

/** POST /v1/auth/invite/accept: an existing account with the link alone; a new person also sets a password (and a name). */
export async function acceptInvite(token: string, password?: string, name?: string) {
  return isLive() ? live.acceptInvite(token, password, name) : (await sample()).acceptInvite();
}

/** PATCH /v1/me: sets the person's own name and returns it as stored (trimmed). */
export async function updateName(name: string) {
  return isLive() ? live.updateName(name) : (await sample()).updateName(name);
}

/** POST /v1/auth/switch-tenant: another of the person's companies, without the password. */
export async function switchTenant(tenantId: string) {
  return isLive() ? live.switchTenant(tenantId) : (await sample()).switchTenant(tenantId);
}

/** After a reload: a new access token from the refresh cookie (live) or the default person (sample). */
export async function restore(area: "portal" | "console") {
  const s = getSession();
  if (s) return s;
  if (isLive()) return live.refresh();
  return (await sample()).restore(area);
}

/**
 * Who is signed in on this browser, or null, without opening a session for anyone new (F33).
 * Live: this tab's memory, else the shared refresh cookie (a refresh; no cookie means nobody).
 * Sample: this tab's memory only, since there is no cookie.
 */
export async function whoIsIn(): Promise<Session | null> {
  const s = getSession();
  if (s || !isLive()) return s;
  return live.refresh().catch(() => null);
}

export async function logout() {
  return isLive() ? live.logout() : (await sample()).logout();
}

/** Clears memory without calling the core, for a session the core already rejected. */
export function forget() {
  setSession(null);
}

// reads

export async function getMyTenant(): Promise<TenantStatus> {
  return isLive() ? live.getJson("/v1/me/tenant") : (await sample()).getMyTenant();
}

export async function listTenants(): Promise<Page<TenantStatus>> {
  return isLive() ? live.getJson("/v1/tenants") : (await sample()).listTenants();
}

export async function listAccounts(): Promise<Page<Account>> {
  return isLive() ? live.getJson("/v1/ledger/accounts") : (await sample()).listAccounts();
}

/** The files that did not enter, newest first: the spec's "Archivos" list, no month filter. */
export async function listQuarantine(): Promise<QuarantineList> {
  return isLive() ? live.getJson("/v1/ledger/files?status=quarantined") : (await sample()).listQuarantine();
}

export async function listMatches(tray: MatchTray, period?: string): Promise<Page<Match>> {
  return isLive() ? live.getJson(`/v1/matching/matches${q({ tray, period })}`) : (await sample()).listMatches(tray, period);
}

export async function listPending(tray: PendingTray, period?: string): Promise<Page<PendingMovement>> {
  return isLive() ? live.getJson(`/v1/matching/pending${q({ tray, period })}`) : (await sample()).listPending(tray, period);
}

/** Open items to link a movement by hand; without period, every open item (oldest first, up to 500). */
export async function listOpenItems(direction: ItemDirection, period?: string): Promise<OpenItems> {
  return isLive() ? live.getJson(`/v1/matching/items${q({ direction, period })}`) : (await sample()).listOpenItems(direction, period);
}

export async function listMetrics(period: string): Promise<Page<Metric>> {
  return isLive() ? live.getJson(`/v1/metrics${q({ period })}`) : (await sample()).listMetrics(period);
}

export async function listExports(): Promise<Page<ExportInfo>> {
  return isLive() ? live.getJson("/v1/exports") : (await sample()).listExports();
}

/** The CSV as a blob, fetched with the bearer token (a plain link would carry no token). */
export async function downloadExport(kind: ExportKind): Promise<{ blob: Blob; filename: string }> {
  if (!isLive()) return (await sample()).downloadExport(kind);
  const res = await live.request(`/v1/exports/${kind}`);
  const name = /filename="?([^";]+)"?/.exec(res.headers.get("Content-Disposition") ?? "")?.[1];
  return { blob: await res.blob(), filename: name ?? `tramo-${kind}.csv` };
}

// members (owner; D-19: adding is always an invitation, pending until accepted)

/** GET /v1/tenant/members: active people first, then pending invitations. */
export async function listMembers(): Promise<Page<Member>> {
  return isLive() ? live.getJson("/v1/tenant/members") : (await sample()).listMembers();
}

/** POST /v1/tenant/members: invites by email with a role; 201 with status pending. */
export async function addMember(email: string, role: Role, idempotencyKey: string): Promise<Member> {
  if (!idempotencyKey) throw new ApiError(400, "bad_request", "Idempotency-Key is required");
  return isLive() ? live.postJson("/v1/tenant/members", { email, role }, idempotencyKey) : (await sample()).addMember(email, role);
}

/** DELETE /v1/tenant/members/{userId}: an active person or a pending invitation; 409 last_owner. */
export async function removeMember(userId: string): Promise<void> {
  if (isLive()) {
    await live.request(`/v1/tenant/members/${encodeURIComponent(userId)}`, { method: "DELETE" });
    return;
  }
  return (await sample()).removeMember(userId);
}

// decisions

export async function decideMatch(matchId: string, body: DecisionRequest, idempotencyKey: string): Promise<Decision> {
  if (!idempotencyKey) throw new ApiError(400, "bad_request", "Idempotency-Key is required");
  return isLive()
    ? live.postJson(`/v1/matching/matches/${encodeURIComponent(matchId)}/decision`, body, idempotencyKey)
    : (await sample()).decideMatch(matchId, body, idempotencyKey);
}

export async function decideMovement(movementId: string, body: MovementDecisionRequest, idempotencyKey: string): Promise<MovementDecision> {
  if (!idempotencyKey) throw new ApiError(400, "bad_request", "Idempotency-Key is required");
  return isLive()
    ? live.postJson(`/v1/matching/movements/${encodeURIComponent(movementId)}/decision`, body, idempotencyKey)
    : (await sample()).decideMovement(movementId, body, idempotencyKey);
}

// rules the UI applies (the core enforces them; the UI only hides what a role cannot do)

/** product/ESPECIFICACION.md section 1. Automático is decided by nobody. */
export function can(role: Role, action: "decideException" | "decideSuggested" | "download") {
  if (action === "decideException") return role === "owner" || role === "approver";
  if (action === "decideSuggested") return role === "operator";
  return role === "owner" || role === "accountant" || role === "operator";
}

/** Where a person lands after logging in. */
export function homeFor(role: Role) {
  return role === "operator" || role === "accountant" ? "/operador" : "/app";
}

export function areaAllows(area: "portal" | "console", role: Role) {
  return area === "console" ? homeFor(role) === "/operador" : homeFor(role) === "/app";
}

export function missingSteps(t: Pick<TenantStatus, "steps">) {
  return t.steps.filter((s) => s.status !== "verified").length;
}

/**
 * Turns a metric's `list` (an API path) into a screen of this area, when there is one.
 * The client only has its Exceptions inbox; the console has the three trays.
 */
export function metricHref(list: string, area: "portal" | "console") {
  const [path, query = ""] = list.split("?");
  const tray = new URLSearchParams(query).get("tray");
  if (path !== "/v1/matching/matches" && path !== "/v1/matching/pending") return undefined;
  if (area === "portal") return tray === "exception" ? "/app/aprobaciones" : undefined;
  return tray ? `/operador/bandejas?bandeja=${tray}` : undefined;
}

/** The current month, YYYY-MM, in Argentina (UTC-3, no daylight saving). Sample mode is pinned. */
export function currentPeriod() {
  if (!isLive()) return "2026-11";
  return new Date(Date.now() - 3 * 3_600_000).toISOString().slice(0, 7);
}

export const periodPattern = /^20\d{2}-(0[1-9]|1[0-2])$/;
