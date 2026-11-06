import type { Problem } from "./types";

/** A non 2xx answer from the core (or from the sample backend, which mimics it). */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  /** Seconds, from the Retry-After header of a 429. */
  readonly retryAfter?: number;

  constructor(status: number, code: string, detail?: string, retryAfter?: number) {
    super(detail ?? code);
    this.status = status;
    this.code = code;
    this.retryAfter = retryAfter;
  }

  static async from(res: Response) {
    let p: Partial<Problem> = {};
    try {
      p = (await res.json()) as Partial<Problem>;
    } catch {
      // Not problem+json (a proxy error page, for instance): keep the status only.
    }
    const ra = Number(res.headers.get("Retry-After"));
    return new ApiError(res.status, p.code ?? "unknown", p.detail, Number.isFinite(ra) && ra > 0 ? ra : undefined);
  }
}

/** The session ended and could not be renewed; the caller is sent back to /entrar. */
export class SessionExpired extends Error {
  constructor() {
    super("session expired");
  }
}
