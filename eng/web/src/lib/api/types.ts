/**
 * Hand-written mirror of the schemas in eng/core/api/openapi.yaml (components.schemas).
 * Names and fields follow the spec exactly; when the spec changes, this file changes with it.
 * No code generator on purpose: no new dependency, and the file is small enough to review.
 *
 * Conventions from the spec: amounts are integer cents (credits positive, debits negative),
 * dates and times are ISO 8601, lists come wrapped as { items, nextCursor }, errors are
 * application/problem+json with a stable `code`. The tenant always comes from the token.
 */

export type Page<T> = { items: T[]; nextCursor?: string | null };

export type Problem = {
  type: string;
  title: string;
  status: number;
  code: string;
  detail?: string;
  requestId?: string;
  /** On login's 429 for the email and IP pair: the person can still get in by magic link. */
  magicLink?: boolean;
};

export type Role = "owner" | "approver" | "viewer" | "operator" | "accountant";
export type Cuit = string;
export type Cents = number;

// identity

export type TokenDelivery = "cookie" | "body";

export type LoginRequest = {
  email: string;
  password: string;
  /** Tenant to act on. Defaults to the oldest membership. */
  tenantId?: string;
  tokenDelivery?: TokenDelivery;
};

export type Tokens = {
  accessToken: string;
  tokenType: "Bearer";
  /** Seconds. */
  expiresIn: number;
  /** Only with tokenDelivery body; the web always uses the cookie. */
  refreshToken?: string;
  tenantId: string;
  role: Role;
};

export type Membership = { tenantId: string; tenantName: string; role: Role };

/** POST /v1/auth/invite/preview (F38): what the invitation is for. Does not spend the token. */
export type InvitePreview = { tenantName: string; role: Role };

/** GET /v1/tenant/members item. pending until the person accepts the invitation (D-19); a pending person's name is never shown. */
export type Member = { userId: string; email: string; name: string | null; role: Role; status: "active" | "pending" };

export type Me = {
  userId: string;
  email: string;
  /** Null until the person sets one; show the email then. */
  name: string | null;
  activeTenantId: string;
  role: Role;
  memberships: Membership[];
};

// portal

export type StepKind = "arca" | "bank" | "erp";
export type StepStatus = "verified" | "pending" | "failed";

export type OnboardingStep = {
  kind: StepKind;
  status: StepStatus;
  /** Why the step has this status, in English, for the UI to translate. */
  detail: string;
  verifiedAt?: string | null;
};

export type TenantStatus = {
  id: string;
  name: string;
  cuit?: Cuit | null;
  /** The caller's role in this tenant. */
  role: Role;
  steps: OnboardingStep[];
  openExceptions: number;
  openExceptionsCapped: boolean;
};

export type MetricUnit = "percent" | "count" | "cents" | "days" | "hours";

export type Metric = {
  key: string;
  label: string;
  /** no_data is not zero and is shown differently. */
  status: "measured" | "no_data";
  /** Null when status is no_data. */
  value: number | null;
  unit: MetricUnit;
  period: string;
  source: string;
  /** API path that returns the rows behind the number. */
  list: string;
};

export type ExportKind = "movements" | "matches" | "audit";

export type ExportInfo = {
  kind: ExportKind;
  format: "csv";
  available: boolean;
  rows?: number | null;
  updatedAt?: string | null;
};

// ledger

export type Bank = "galicia" | "santander" | "bancor" | "macro" | "mercadopago";

export type Account = { id: string; bank: Bank; number?: string; name: string; currency: string };

export type QuarantinedFile = {
  id: string;
  accountId: string;
  filename?: string;
  reason: string;
  detail?: string;
  fingerprint?: string;
  receivedAt: string;
};

// matching

export type Adjustment = {
  kind: string;
  amount: Cents;
  source: "documented_by_source" | "rounding_tolerance";
};

export type Match = {
  id: string;
  rule: string;
  ruleVersion: number;
  /** Where the match waits now; a suggested match an operator escalated is in exception. */
  tray: "auto" | "suggested" | "exception";
  movementIds: string[];
  itemIds: string[];
  adjustments: Adjustment[];
  reference?: string;
  dateDiffDays?: number;
  crossPeriod: boolean;
  lowConfidence: boolean;
};

export type Candidate = { rule: string; itemIds: string[] };

/** An item nothing settles yet (GET /v1/matching/items), what "Vincular a mano" offers. */
export type OpenItem = {
  id: string;
  /** erp, document or manual. */
  source: string;
  sourceId: string;
  kind: string;
  date: string;
  due?: string;
  /** Cents, with the sign it will have in the bank. */
  amount: Cents;
  refs: string[];
  /** The CUIT on the other side, when known. */
  party?: string;
  confidence: number;
  invoiceId?: string;
};
export type OpenItems = { items: OpenItem[]; truncated: boolean };
export type ItemDirection = "in" | "out";

export type PendingReason =
  | "ambiguous" | "shared_candidate" | "combination_ambiguous" | "reference_amount_differs" | "no_candidate"
  | "client_other" | "client_rejected" | "operator_returned";

export type PendingMovement = {
  movementId: string;
  runId: string;
  /** Date of the movement. */
  date: string;
  amount: Cents;
  /** As the bank wrote it. */
  description: string;
  accountId: string;
  accountName: string;
  bank: Bank;
  tray: "suggested" | "exception";
  reason: PendingReason;
  /** The client's note for client_other and client_rejected, the operator's for operator_returned. */
  note?: string;
  /** The match the client rejected, for client_rejected. */
  matchId?: string;
  candidates: Candidate[];
};

/** Query value of GET /v1/matching/matches. */
export type MatchTray = "auto" | "suggested" | "exception";
/** Query value of GET /v1/matching/pending. */
export type PendingTray = "suggested" | "exception";

export type DecisionAction = "approve" | "reject" | "escalate" | "other" | "revert";
export type DecisionRequest = { action: DecisionAction; note?: string };

export type Decision = {
  matchId: string;
  tray: "suggested" | "exception";
  action: DecisionAction;
  userId: string;
  role: Role;
  at: string;
  note?: string;
  /** Per match and tray; a revert is a new revision. */
  revision: number;
};

/** other and link: the client. resolve, return and link: the operator, once the client said other. */
export type MovementDecisionAction = "other" | "link" | "resolve" | "return" | "revert";
export type MovementDecisionRequest = { action: MovementDecisionAction; note?: string; itemIds?: string[] };

export type MovementDecision = {
  movementId: string;
  action: MovementDecisionAction;
  matchId?: string;
  note?: string;
  userId: string;
  role: Role;
  at: string;
  revision: number;
};
