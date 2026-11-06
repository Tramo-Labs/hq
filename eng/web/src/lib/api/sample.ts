import { ApiError } from "./errors";
import * as fx from "./fixtures";
import { getSession, setSession, type Session } from "./session";
import type {
  Decision, DecisionRequest, ExportInfo, ExportKind, Match, MatchTray, Metric, MovementDecision, MovementDecisionRequest,
  Page, PendingMovement, PendingTray, Role, TenantStatus,
} from "./types";

/**
 * Sample backend: answers like the core does (same shapes, same status codes for the cases
 * the UI handles) from the synthetic fixtures. Loaded only when CORE_API_URL is unset.
 * State lives in memory and resets on reload.
 */

const page = <T,>(items: T[]): Page<T> => ({ items, nextCursor: null });
const userId = (email: string) => `00000000-0000-4000-9000-${String(fx.users.findIndex((u) => u.email === email) + 1).padStart(12, "0")}`;

function session() {
  const s = getSession();
  if (!s) throw new ApiError(401, "unauthorized");
  return s;
}

function open(email: string, tenantId?: string): Session {
  const ms = fx.memberships(email);
  const m = ms.find((x) => x.tenantId === tenantId) ?? ms[0];
  const s: Session = { accessToken: "", tenantId: m.tenantId, tenantName: m.tenantName, role: m.role, email, memberships: ms };
  setSession(s);
  return s;
}

/** Throttle like the core: 5 failures per email lock it for a while (429 with Retry-After). */
const failures = new Map<string, { n: number; until: number }>();

export async function login(email: string, password: string, tenantId?: string) {
  const key = email.trim().toLowerCase();
  const f = failures.get(key);
  if (f && f.until > Date.now()) throw new ApiError(429, "rate_limited", undefined, Math.ceil((f.until - Date.now()) / 1000));
  const known = fx.users.some((u) => u.email === key);
  if (!known || password !== fx.samplePassword) {
    const n = (f?.n ?? 0) + 1;
    failures.set(key, { n, until: n >= 5 ? Date.now() + 15 * 60_000 : 0 });
    throw new ApiError(401, "unauthorized");
  }
  failures.delete(key);
  return open(key, tenantId);
}

/** Sample mode has no cookie: before any login, the area's default person is in. */
export async function restore(area: "portal" | "console") {
  return getSession() ?? open(fx.defaultUser[area]);
}

export async function logout() {
  setSession(null);
}

// Decisions recorded in this tab, by match or movement id, and their idempotency keys.
const decided = new Map<string, Decision | MovementDecision>();
const replies = new Map<string, Decision | MovementDecision>();

function status(t: fx.SampleTenant, role: Role): TenantStatus {
  const open =
    fx.matches.filter((x) => x.tenantId === t.id && x.listed === "exception" && !decided.has(x.match.id)).length +
    fx.pending.filter((x) => x.tenantId === t.id && x.pending.tray === "exception" && !decided.has(x.pending.movementId)).length;
  return { ...t, role, openExceptions: open, openExceptionsCapped: false };
}

export async function getMyTenant() {
  const s = session();
  return status(fx.tenants.find((t) => t.id === s.tenantId)!, s.role);
}

export async function listTenants() {
  const s = session();
  const mine = fx.memberships(s.email).filter((m) => m.role === "operator" || m.role === "accountant");
  if (mine.length === 0) throw new ApiError(403, "forbidden");
  return page(mine.map((m) => status(fx.tenants.find((t) => t.id === m.tenantId)!, m.role)));
}

export async function listAccounts() {
  return page(fx.accounts[session().tenantId] ?? []);
}

export async function listQuarantine() {
  return page(fx.quarantine[session().tenantId] ?? []);
}

/** The client (owner, approver, viewer) does not see Suggested: that tray is the operator's. */
const seesSuggested = (role: Role) => role === "operator" || role === "accountant";

export async function listMatches(tray: MatchTray, period?: string) {
  const s = session();
  if (tray === "suggested" && !seesSuggested(s.role)) throw new ApiError(403, "forbidden");
  return page<Match>(
    fx.matches
      .filter((x) => x.tenantId === s.tenantId && x.listed === tray && (!period || x.period === period) && !decided.has(x.match.id))
      .map((x) => x.match),
  );
}

export async function listPending(tray: PendingTray, period?: string) {
  const s = session();
  if (tray === "suggested" && !seesSuggested(s.role)) throw new ApiError(403, "forbidden");
  return page<PendingMovement>(
    fx.pending
      .filter((x) => x.tenantId === s.tenantId && x.pending.tray === tray && (!period || x.period === period) && !decided.has(x.pending.movementId))
      .map((x) => x.pending),
  );
}

const deciders: Record<"suggested" | "exception", Role[]> = { suggested: ["operator"], exception: ["owner", "approver"] };
const offered: Record<"suggested" | "exception", string[]> = { suggested: ["approve", "reject", "escalate", "revert"], exception: ["approve", "reject", "other", "revert"] };

function replay<T extends Decision | MovementDecision>(key: string, make: () => T): T {
  if (!key) throw new ApiError(400, "bad_request", "Idempotency-Key is required");
  const prior = replies.get(key);
  if (prior) return prior as T;
  const out = make();
  replies.set(key, out);
  return out;
}

export async function decideMatch(matchId: string, body: DecisionRequest, idempotencyKey: string): Promise<Decision> {
  const s = session();
  return replay(idempotencyKey, () => {
    const x = fx.matches.find((y) => y.tenantId === s.tenantId && y.match.id === matchId);
    if (!x || x.listed === "auto") throw new ApiError(404, "not_found");
    const tray = x.listed;
    if (!deciders[tray].includes(s.role)) throw new ApiError(403, "forbidden");
    if (!offered[tray].includes(body.action)) throw new ApiError(400, "bad_request");
    if ((body.action === "other" || body.action === "reject") && !body.note?.trim()) throw new ApiError(400, "bad_request", "note required");
    const prior = decided.get(matchId) as Decision | undefined;
    if (body.action === "revert") {
      if (!prior) throw new ApiError(409, "nothing_to_revert");
      decided.delete(matchId);
    } else if (prior) throw new ApiError(409, "conflict");
    const d: Decision = { matchId, tray, action: body.action, userId: userId(s.email), role: s.role, at: new Date().toISOString(),
      note: body.note, revision: (prior?.revision ?? 0) + 1 };
    if (body.action !== "revert") decided.set(matchId, d);
    return d;
  });
}

export async function decideMovement(movementId: string, body: MovementDecisionRequest, idempotencyKey: string): Promise<MovementDecision> {
  const s = session();
  return replay(idempotencyKey, () => {
    const x = fx.pending.find((y) => y.tenantId === s.tenantId && y.pending.movementId === movementId);
    if (!x) throw new ApiError(404, "not_found");
    if (!deciders.exception.includes(s.role)) throw new ApiError(403, "forbidden");
    if (x.pending.tray !== "exception") throw new ApiError(409, "not_in_exceptions");
    if (body.action === "other" && !body.note?.trim()) throw new ApiError(400, "bad_request", "note required");
    if (body.action === "link") throw new ApiError(400, "bad_request", "sample mode has no open items to link");
    const prior = decided.get(movementId) as MovementDecision | undefined;
    if (body.action === "revert") {
      if (!prior) throw new ApiError(409, "nothing_to_revert");
      decided.delete(movementId);
    } else if (prior) throw new ApiError(409, "conflict");
    const d: MovementDecision = { movementId, action: body.action, userId: userId(s.email), role: s.role, at: new Date().toISOString(),
      note: body.note, revision: (prior?.revision ?? 0) + 1 };
    if (body.action !== "revert") decided.set(movementId, d);
    return d;
  });
}

/**
 * Same keys, units, sources and lists as the core's metrics (internal/matching/README.md),
 * computed from the fixtures. A month with nothing stored is no_data, never zero.
 */
export async function listMetrics(period: string) {
  const s = session();
  const moves = fx.movementCount[s.tenantId]?.[period];
  const inMonth = fx.matches.filter((x) => x.tenantId === s.tenantId && x.period === period);
  const pend = fx.pending.filter((x) => x.tenantId === s.tenantId && x.period === period);
  const run = moves !== undefined;
  const noRun = "Todavía no se corrió la conciliación de este mes";
  const metric = (key: string, label: string, unit: Metric["unit"], list: string, value: number | null, source: string): Metric =>
    ({ key, label, unit, list, period, source, value, status: value === null ? "no_data" : "measured" });
  const auto = inMonth.filter((x) => x.listed === "auto").length;
  const openMoves = pend.filter((x) => x.pending.tray === "exception" && !decided.has(x.pending.movementId)).length;
  const openMatches = inMonth.filter((x) => x.listed === "exception" && !decided.has(x.match.id)).length;
  const items: Metric[] = [
    metric("automation_rate", "Conciliación automática", "percent", `/v1/matching/matches?tray=auto&period=${period}`,
      run && moves > 0 ? Math.round((1000 * auto) / moves) / 10 : null,
      run ? `${auto} movimientos conciliados por una regla exacta sobre ${moves} movimientos del mes` : noRun),
    metric("open_exception_movements", "Movimientos sin explicar", "count", `/v1/matching/pending?tray=exception&period=${period}`,
      run ? openMoves : null, run ? "Movimientos sin candidato que esperan tu decisión" : noRun),
    metric("open_exception_matches", "Sugerencias esperando al cliente", "count", `/v1/matching/matches?tray=exception&period=${period}`,
      run ? openMatches : null, run ? "Sugerencias que el operador te pasó y todavía no decidiste" : noRun),
    metric("oldest_open_exception_days", "Antigüedad de la excepción más vieja", "days", `/v1/matching/pending?tray=exception&period=${period}`,
      run && openMoves > 0 ? fx.oldestOpenDays[s.tenantId] ?? 0 : null,
      run ? (openMoves > 0 ? "Días desde el movimiento sin explicar más viejo" : "No hay movimientos sin explicar") : noRun),
    metric("invoices_processed", "Facturas procesadas", "count", `/v1/documents/invoices?period=${period}`, null, "Todavía no se cargó ninguna factura"),
  ];
  return page(items);
}

const csv: Record<ExportKind, (tenantId: string) => string[][]> = {
  movements: () => [["movement_id", "date", "amount"]],
  matches: (t) => [
    ["match_id", "rule", "rule_version", "tray", "movement_ids", "item_ids"],
    ...fx.matches.filter((x) => x.tenantId === t).map((x) => [x.match.id, x.match.rule, String(x.match.ruleVersion), x.match.tray, x.match.movementIds.join(" "), x.match.itemIds.join(" ")]),
  ],
  audit: () => [],
};

export async function listExports() {
  const s = session();
  const rows = (k: ExportKind) => csv[k](s.tenantId).length - 1;
  const items: ExportInfo[] = [
    { kind: "movements", format: "csv", available: false, rows: null },
    { kind: "matches", format: "csv", available: rows("matches") > 0, rows: Math.max(0, rows("matches")) },
    { kind: "audit", format: "csv", available: false, rows: null },
  ];
  return page(items);
}

export async function downloadExport(kind: ExportKind) {
  const s = session();
  const rows = csv[kind](s.tenantId);
  if (rows.length === 0) throw new ApiError(501, "not_implemented");
  const text = rows.map((r) => r.join(",")).join("\n") + "\n";
  return { blob: new Blob([text], { type: "text/csv" }), filename: `tramo-${kind}-ejemplo.csv` };
}
