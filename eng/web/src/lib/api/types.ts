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
  | "exact" | "recurring"
  | "tolerance" | "combination" | "model"
  | "no_candidate" | "duplicate" | "balance" | "unauthorized_supplier" | "over_threshold" | "not_verified";

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

export type MetricKey = "auto_rate" | "open_exceptions" | "oldest_exception" | "bank_vs_books" | "invoices" | "duplicates";
export type Metric = {
  key: MetricKey;
  /** null means "no data yet", which is shown differently from zero. */
  value: number | null;
  unit: "percent" | "count" | "days" | "ars";
  period: string;
  /** Table, filter and formula the number comes from. */
  source: string;
  /** The list behind the number. A number without one does not go on the panel. */
  href?: string;
};

export type UploadedFile = {
  name: string;
  kind: "statement" | "document";
  uploadedAt: string;
  by: string;
  status: "ready" | "problem" | "duplicate" | "quarantine";
};
