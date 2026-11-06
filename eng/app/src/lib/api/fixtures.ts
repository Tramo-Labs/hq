import type { Account, Match, Membership, OpenItem, PendingMovement, QuarantinedFile, Role, TenantStatus } from "./types";

/**
 * Synthetic data for sample mode (no CORE_API_URL), in the exact shapes of
 * eng/core/api/openapi.yaml. Every company, CUIT, email, id and amount is invented: CUITs
 * use the zeros pattern and emails the reserved .example domain. The UI labels all of it
 * as sample data. Never put a real customer here.
 */

/** Fixed "today" so sample ages and the default month do not drift between builds. */
export const asOf = "2026-11-06";

/** Sample login only: the core is not involved and no real account uses it. */
export const samplePassword = "ejemplo";

const id = (n: number) => `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`;
const hex = (n: number) => n.toString(16).padStart(32, "0");

export const T = { agro: id(1), frio: id(2), ferr: id(3), trans: id(4) };

export type SampleTenant = Omit<TenantStatus, "role" | "openExceptions" | "openExceptionsCapped">;

export const tenants: SampleTenant[] = [
  {
    id: T.agro, name: "Agroinsumos del Sur SA", cuit: "30000000015",
    steps: [
      { kind: "arca", status: "pending", detail: "CUIT on file; ARCA verification is not connected yet" },
      { kind: "bank", status: "verified", detail: "statement accepted for Galicia CC", verifiedAt: "2026-11-03T13:10:00Z" },
      { kind: "erp", status: "pending", detail: "no ERP connector available yet" },
    ],
  },
  {
    id: T.frio, name: "Frigorífico La Pampa SRL", cuit: null,
    steps: [
      { kind: "arca", status: "pending", detail: "CUIT not on file yet" },
      { kind: "bank", status: "pending", detail: "no bank account yet" },
      { kind: "erp", status: "pending", detail: "no ERP connector available yet" },
    ],
  },
  {
    id: T.ferr, name: "Ferretería Industrial Norte", cuit: "30000000023",
    steps: [
      { kind: "arca", status: "pending", detail: "CUIT on file; ARCA verification is not connected yet" },
      { kind: "bank", status: "pending", detail: "account created; no statement accepted yet" },
      { kind: "erp", status: "pending", detail: "no ERP connector available yet" },
    ],
  },
  {
    id: T.trans, name: "Transportes Río Cuarto SA", cuit: "30000000031",
    steps: [
      { kind: "arca", status: "pending", detail: "CUIT on file; ARCA verification is not connected yet" },
      { kind: "bank", status: "verified", detail: "statement accepted for Santander CC", verifiedAt: "2026-11-04T11:00:00Z" },
      { kind: "erp", status: "pending", detail: "no ERP connector available yet" },
    ],
  },
];

/** Sample people. Any of them logs in with samplePassword. */
export const users: { email: string; memberships: { tenantId: string; role: Role }[] }[] = [
  { email: "titular@agro.example", memberships: [{ tenantId: T.agro, role: "owner" }] },
  { email: "aprobador@agro.example", memberships: [{ tenantId: T.agro, role: "approver" }] },
  { email: "lectura@agro.example", memberships: [{ tenantId: T.agro, role: "viewer" }] },
  { email: "contador@estudio.example", memberships: [{ tenantId: T.ferr, role: "accountant" }, { tenantId: T.trans, role: "accountant" }] },
  { email: "operador@tramo.example", memberships: [T.agro, T.frio, T.ferr, T.trans].map((tenantId) => ({ tenantId, role: "operator" as const })) },
];

export const memberships = (email: string): Membership[] =>
  (users.find((u) => u.email === email)?.memberships ?? []).map((m) => ({ ...m, tenantName: tenants.find((t) => t.id === m.tenantId)!.name }));

/** Who the portal and the console show before anyone logs in, in sample mode. */
export const defaultUser = { portal: "titular@agro.example", console: "operador@tramo.example" } as const;

export const accounts: Record<string, Account[]> = {
  [T.agro]: [
    { id: id(101), bank: "galicia", number: "0000000000004021", name: "Galicia CC", currency: "ARS" },
    { id: id(102), bank: "macro", number: "0000000000007730", name: "Macro CA USD", currency: "USD" },
  ],
  [T.ferr]: [{ id: id(103), bank: "bancor", number: "0000000000001188", name: "Bancor CC", currency: "ARS" }],
  [T.trans]: [
    { id: id(104), bank: "santander", number: "0000000000005502", name: "Santander CC", currency: "ARS" },
    { id: id(105), bank: "mercadopago", name: "Mercado Pago", currency: "ARS" },
  ],
};

export const quarantine: Record<string, QuarantinedFile[]> = {
  [T.agro]: [
    { id: id(201), accountId: id(102), filename: "macro-usd-octubre.xls", reason: "balance_mismatch",
      detail: "closing balance of line 41 does not follow from the opening balance", receivedAt: "2026-11-03T14:02:00Z" },
    { id: id(203), accountId: id(101), filename: "galicia-octubre.docx", reason: "unsupported_format",
      detail: "docx is not a statement file kind", receivedAt: "2026-11-04T10:15:00Z" },
    { id: id(204), accountId: id(101), filename: "galicia-septiembre.csv", reason: "period_closed",
      detail: "new movements dated in the closed period 2026-09-16..2026-09-30", receivedAt: "2026-11-05T16:40:00Z" },
    { id: id(205), accountId: id(102), filename: "macro-usd-noviembre.csv", reason: "movement_conflict",
      detail: "line 12 has the identity of a stored movement with another description", receivedAt: "2026-11-06T11:20:00Z" },
  ],
  [T.ferr]: [
    { id: id(202), accountId: id(103), filename: "bancor-octubre.pdf", reason: "unknown_layout",
      detail: "header fingerprint 3f9a is not a known layout", receivedAt: "2026-11-05T09:30:00Z" },
  ],
};

/** A match plus where the sample lists it and the month of its movement. */
export type SampleMatch = { tenantId: string; listed: "auto" | "suggested" | "exception"; period: string; match: Match };

const m = (n: number, rule: string, tray: Match["tray"], extra: Partial<Match> = {}): Match => ({
  id: hex(n), rule, ruleVersion: 1, tray, movementIds: [id(1000 + n)], itemIds: [id(2000 + n)],
  adjustments: [], crossPeriod: false, lowConfidence: false, ...extra,
});

export const matches: SampleMatch[] = [
  { tenantId: T.agro, listed: "auto", period: "2026-11", match: m(1, "reference_exact", "auto", { reference: "0003-00001207", dateDiffDays: 0 }) },
  { tenantId: T.agro, listed: "auto", period: "2026-11", match: m(2, "amount_date_window", "auto", { dateDiffDays: 2 }) },
  { tenantId: T.agro, listed: "auto", period: "2026-11", match: m(3, "reference_exact", "auto", { reference: "0003-00001215", dateDiffDays: 1 }) },
  { tenantId: T.agro, listed: "suggested", period: "2026-11", match: m(4, "amount_tolerance", "suggested", {
    dateDiffDays: 1, adjustments: [{ kind: "rounding", amount: -700, source: "rounding_tolerance" }] }) },
  { tenantId: T.agro, listed: "exception", period: "2026-11", match: m(5, "one_movement_many_items", "exception", {
    itemIds: [id(2005), id(2105)], dateDiffDays: 3 }) },
  { tenantId: T.trans, listed: "auto", period: "2026-11", match: m(6, "reference_exact", "auto", { reference: "MP 81234567890", dateDiffDays: 0 }) },
  { tenantId: T.trans, listed: "suggested", period: "2026-11", match: m(7, "net_of_documented_fees", "suggested", {
    reference: "MP 81234567999", adjustments: [{ kind: "mp_fee", amount: -1_749_00, source: "documented_by_source" }] }) },
  { tenantId: T.ferr, listed: "suggested", period: "2026-11", match: m(8, "many_movements_one_item", "suggested", {
    movementIds: [id(1008), id(1108)], dateDiffDays: 4, lowConfidence: true }) },
];

export type SamplePending = { tenantId: string; period: string; pending: PendingMovement };

export const pending: SamplePending[] = [
  { tenantId: T.agro, period: "2026-11", pending: { movementId: id(3001), runId: id(4001), date: "2026-11-04", amount: -18_450_000,
    description: "TRANSF A 30-00000000-1 VARIOS", accountId: id(101), accountName: "Galicia CC", bank: "galicia",
    tray: "exception", reason: "no_candidate", candidates: [] } },
  { tenantId: T.agro, period: "2026-11", pending: { movementId: id(3002), runId: id(4001), date: "2026-11-05", amount: 4_235_000,
    description: "CREDITO TRANSF 30-00000000-2", accountId: id(101), accountName: "Galicia CC", bank: "galicia",
    tray: "suggested", reason: "ambiguous",
    candidates: [{ rule: "amount_date_window", itemIds: [id(2201)] }, { rule: "amount_date_window", itemIds: [id(2202)] }] } },
  { tenantId: T.ferr, period: "2026-11", pending: { movementId: id(3003), runId: id(4002), date: "2026-11-03", amount: -912_000,
    description: "DEBITO AUTOMATICO SERVICIO", accountId: id(103), accountName: "Bancor CC", bank: "bancor",
    tray: "exception", reason: "no_candidate", candidates: [] } },
  { tenantId: T.ferr, period: "2026-11", pending: { movementId: id(3004), runId: id(4002), date: "2026-11-02", amount: -2_600_000,
    description: "TRANSF A 20-00000000-3", accountId: id(103), accountName: "Bancor CC", bank: "bancor",
    tray: "suggested", reason: "client_other", note: "Adelanto a un proveedor, todavía sin factura", candidates: [] } },
  { tenantId: T.agro, period: "2026-11", pending: { movementId: id(3005), runId: id(4001), date: "2026-11-06", amount: -1_230_000,
    description: "TRANSF A 30-00000000-4", accountId: id(101), accountName: "Galicia CC", bank: "galicia",
    tray: "exception", reason: "operator_returned", note: "Necesito el número de factura o el nombre del proveedor", candidates: [] } },
  { tenantId: T.agro, period: "2026-11", pending: { movementId: id(3006), runId: id(4001), date: "2026-11-06", amount: 980_000,
    description: "CREDITO TRANSF 30-00000000-5", accountId: id(101), accountName: "Galicia CC", bank: "galicia",
    tray: "suggested", reason: "shared_candidate", candidates: [{ rule: "amount_date_window", itemIds: [id(2203)] }] } },
];

/** Movements per tenant and month, for the automation rate and the movements export. */
/**
 * Open items per tenant, for "Vincular a mano". Two of agro's add up exactly to movement 3001
 * (-12.000.000 + -6.450.000 = -18.450.000 cents), one of ferr's to 3003 and two to 3004.
 */
const item = (n: number, date: string, amount: number, refs: string[], party?: string): OpenItem => ({
  id: id(n), source: "document", sourceId: id(n + 1000), kind: amount < 0 ? "payable" : "receivable", date, amount, refs, party, confidence: 1,
});
export const openItems: Record<string, OpenItem[]> = {
  [T.agro]: [
    item(5001, "2026-10-28", -1_230_000, ["FC A 0003-00000412"], "30000000041"),
    item(5002, "2026-11-02", -12_000_000, ["FC A 0001-00001234"], "30000000011"),
    item(5003, "2026-11-03", -6_450_000, ["FC A 0001-00001240"], "30000000011"),
    item(5004, "2026-11-05", -3_000_000, ["FC B 0002-00000077"]),
    item(5005, "2026-11-04", 4_235_000, ["FC A 0005-00000900"], "30000000021"),
  ],
  [T.ferr]: [
    item(5101, "2026-11-01", -912_000, ["FC C 0004-00000031"], "30000000051"),
    item(5102, "2026-11-01", -2_000_000, ["FC A 0006-00000210"], "20000000031"),
    item(5103, "2026-11-02", -600_000, ["FC A 0006-00000211"], "20000000031"),
  ],
};

export const movementCount: Record<string, Record<string, number>> = {
  [T.agro]: { "2026-11": 7 },
  [T.trans]: { "2026-11": 2 },
  [T.ferr]: { "2026-11": 4 },
};

/** Oldest open exception movement, in days, per tenant (sample). */
export const oldestOpenDays: Record<string, number> = { [T.agro]: 6, [T.ferr]: 7 };
