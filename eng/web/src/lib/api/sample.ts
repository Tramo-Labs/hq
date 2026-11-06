import { ApiError } from "./errors";
import * as fx from "./fixtures";
import { getSession, setSession, type Session } from "./session";
import type {
  Decision, DecisionRequest, ExportInfo, ExportKind, Match, MatchTray, Metric, MovementDecision, MovementDecisionRequest,
  Page, PendingMovement, PendingTray, Role, TenantStatus, ItemDirection, OpenItems,
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

/** Sample mode sends no email; it answers like the core (202, nothing to report). */
export async function requestMagicLink() {}

/** Sample mode issues no links, so any token is unknown, as the core answers for one. */
export async function acceptMagicLink(): Promise<Session> {
  throw new ApiError(401, "invalid_magic_link");
}

/** Sample mode sends no invitations either: any token is unknown (401 invalid_invite). */
export async function acceptInvite(): Promise<Session> {
  throw new ApiError(401, "invalid_invite");
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

/** Suggested is the operator's alone, like the core: everyone else gets 403, accountant included. */
const seesSuggested = (role: Role) => role === "operator";

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
    allPending()
      .filter((x) => x.tenantId === s.tenantId && x.pending.tray === tray && (!period || x.period === period) && !decided.has(x.pending.movementId))
      .map((x) => x.pending),
  );
}

// A client's reject in Exceptions sends each movement of the match back to the operator, in
// Suggested with reason client_rejected (datos' core at c318326). Synthetic movement details.
const rejectedBack = new Map<string, fx.SamplePending[]>();
const allPending = () => [...fx.pending, ...[...rejectedBack.values()].flat()];

function sendBack(x: (typeof fx.matches)[number], note: string | undefined) {
  const account = fx.accounts[x.tenantId]?.[0];
  rejectedBack.set(x.match.id, x.match.movementIds.map((movementId, i) => ({
    tenantId: x.tenantId, period: x.period,
    pending: { movementId, runId: x.match.id, date: `${x.period}-0${i + 3}`, amount: -1_500_000, description: "MOVIMIENTO DE EJEMPLO",
      accountId: account?.id ?? "", accountName: account?.name ?? "", bank: account?.bank ?? "galicia",
      tray: "suggested", reason: "client_rejected", note, matchId: x.match.id, candidates: [] },
  })));
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
      const back = rejectedBack.get(matchId) ?? [];
      if (back.some((m) => decided.has(m.pending.movementId))) throw new ApiError(409, "operator_answered");
      rejectedBack.delete(matchId);
      decided.delete(matchId);
    } else if (prior) throw new ApiError(409, "conflict");
    const d: Decision = { matchId, tray, action: body.action, userId: userId(s.email), role: s.role, at: new Date().toISOString(),
      note: body.note, revision: (prior?.revision ?? 0) + 1 };
    if (body.action !== "revert") decided.set(matchId, d);
    if (body.action === "reject" && tray === "exception") sendBack(x, body.note);
    return d;
  });
}

/** Items linked by hand in this tab, by item id, to the movement that took them. */
const linked = new Map<string, string>();

export async function listOpenItems(direction: ItemDirection, period?: string): Promise<OpenItems> {
  const s = session();
  const items = (fx.openItems[s.tenantId] ?? [])
    .filter((o) => (direction === "in" ? o.amount > 0 : o.amount < 0) && (!period || o.date.startsWith(period)) && !linked.has(o.id))
    .sort((a, b) => a.date.localeCompare(b.date));
  return { items, truncated: false };
}

export async function decideMovement(movementId: string, body: MovementDecisionRequest, idempotencyKey: string): Promise<MovementDecision> {
  const s = session();
  return replay(idempotencyKey, () => {
    const x = allPending().find((y) => y.tenantId === s.tenantId && y.pending.movementId === movementId);
    if (!x) throw new ApiError(404, "not_found");
    const withOperator = x.pending.reason === "client_other" || x.pending.reason === "client_rejected";
    // Like the core (internal/matching/README.md, F4): the client decides a movement in its
    // Exceptions; once the client said other (client_other), the operator answers it.
    const operator = s.role === "operator";
    if (!operator && !deciders.exception.includes(s.role)) throw new ApiError(403, "forbidden");
    const allowed = operator ? ["resolve", "return", "link", "revert"] : ["other", "link", "revert"];
    if (!allowed.includes(body.action)) throw new ApiError(400, "bad_request");
    if (["other", "resolve", "return"].includes(body.action) && !body.note?.trim()) throw new ApiError(400, "bad_request", "note required");
    const prior = decided.get(movementId) as MovementDecision | undefined;
    if (body.action === "revert") {
      if (!operator && x.pending.reason === "client_rejected") throw new ApiError(409, "revert_on_match");
      if (!prior) throw new ApiError(409, "nothing_to_revert");
      if ((prior.role === "operator") !== operator) throw new ApiError(403, "forbidden");
      decided.delete(movementId);
      for (const [itemId, by] of linked) if (by === movementId) linked.delete(itemId);
    } else {
      if (operator && !withOperator) throw new ApiError(409, "not_with_operator");
      if (!operator && x.pending.tray !== "exception") throw new ApiError(409, "not_in_exceptions");
      if (prior) throw new ApiError(409, "conflict");
      if (body.action === "link") {
        const ids = body.itemIds ?? [];
        if (ids.length === 0 || new Set(ids).size !== ids.length) throw new ApiError(400, "bad_request", "itemIds required");
        const open = fx.openItems[s.tenantId] ?? [];
        const picked = ids.map((i) => open.find((o) => o.id === i));
        if (picked.some((o) => !o)) throw new ApiError(400, "bad_request", "unknown item");
        if (ids.some((i) => linked.has(i))) throw new ApiError(409, "item_taken");
        if (picked.reduce((t, o) => t + o!.amount, 0) !== x.pending.amount) throw new ApiError(400, "bad_request", "amounts do not add up");
        for (const i of ids) linked.set(i, movementId);
      }
    }
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
