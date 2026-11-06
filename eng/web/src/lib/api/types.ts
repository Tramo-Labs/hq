/**
 * Shapes the portal and the operator console consume, following product/ESPECIFICACION.md.
 * They mirror the paths we asked backend to add to eng/core/api/openapi.yaml; once the spec
 * has them, replace this file with generated types and keep the names.
 * Amounts are integer cents. Dates are ISO 8601.
 */

/** Every list comes wrapped so the core can paginate without breaking the screens. */
export type Page<T> = { items: T[]; nextCursor?: string };
/** Errors come as RFC 9457 problem+json. */
export type Problem = { type: string; title: string; status: number; detail?: string; code?: string };

export type Role ="owner" | "approver" | "viewer" | "accountant" | "operator";
export type Session = { name: string; role: Role };

/** Four states per step (spec section 3). A problem always carries a sentence, never a code. */
export type StepStatus = "pending" | "checking" | "ready" | "problem";

export type ArcaStep = { status: StepStatus; problem?: string; confirmedAt?: string };

export type BankAccount = {
  bank: string;
  kind: string;
  currency: "ARS" | "USD";
  last4: string;
  status: StepStatus;
  problem?: string;
  lastStatementAt?: string;
};
export type BankStep = { status: StepStatus; mailbox: string; accounts: BankAccount[] };

export type ErpStep = {
  status: StepStatus;
  system?: string;
  mode?: "api" | "export";
  /** Last characters of the stored credential. The full one is never sent back. */
  credentialTail?: string;
  problem?: string;
};

export type Tenant = {
  id: string;
  name: string;
  cuit: string;
  arca: ArcaStep;
  bank: BankStep;
  erp: ErpStep;
  /** When the tenant started waiting on its oldest missing step. */
  waitingSince?: string;
  openExceptions: number;
};

export type Tray = "auto" | "suggested" | "exception";
export type Outcome = "approved" | "rejected" | "other";

/** Why an item landed in its tray (spec sections 4 and 5). Labels live in content/app.ts. */
export type Motive =
  /** Automático: a unique reference (cheque number, Mercado Pago id, invoice number) is the main pass. */
  | "unique_reference" | "exact" | "recurring"
  | "tolerance" | "combination" | "model"
  /** supplier_or_po: supplier not authorized, or invoice without a purchase order. */
  | "no_candidate" | "duplicate" | "balance" | "supplier_or_po" | "over_threshold" | "not_verified";

export type Movement = { date: string; account: string; description: string; amountCents: number };
export type DocumentRef = { kind: string; number: string; counterparty: string; amountCents: number };

export type Match = {
  id: string;
  tenantId: string;
  tray: Tray;
  motive: Motive;
  openedAt: string;
  /** One sentence: what this is. */
  summary: string;
  /** Why it was not resolved on its own. */
  explanation: string;
  /** What we propose to register, if anything. */
  proposal?: string;
  movement: Movement;
  document?: DocumentRef;
  rule: { id: string; version: number; name: string };
  decision?: { by: string; at: string; outcome: Outcome; note?: string };
};

export type MetricKey = "auto_rate" | "open_exceptions" | "oldest_exception" | "bank_vs_books" | "invoices" | "held_invoices" | "duplicates";

/** The list behind a number, as a tray filter. Each area turns it into its own link. */
export type MetricList = { tray: Tray; motive?: Motive };

export type Metric = {
  key: MetricKey;
  /** null means "no data yet", which is shown differently from zero. */
  value: number | null;
  unit: "percent" | "count" | "days" | "ars";
  period: string;
  /** Table, filter and formula the number comes from. */
  source: string;
  /** A number whose list cannot be opened does not go on the panel (spec section 4). */
  list?: MetricList;
  /** Per-account breakdown, for numbers the spec asks by account. */
  byAccount?: { account: string; value: number | null; list?: MetricList }[];
};

export type UploadedFile = {
  name: string;
  kind: "statement" | "document";
  uploadedAt: string;
  by: string;
  status: "ready" | "problem" | "duplicate" | "quarantine";
};
