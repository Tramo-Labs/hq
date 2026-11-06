import { app } from "../../content/app";
import type { Mode } from "../../lib/api/session";
import type { Metric } from "../../lib/api/types";

/**
 * The tag a number carries on its own card. In sample mode every measured number is an
 * illustrative example, and the card says so by itself: a screenshot of one card must not
 * read as a client's real figure. "No data yet" is not a number, so it carries no tag; live
 * numbers come from the core and carry none either.
 */
export function metricTag(mode: Mode, m: Pick<Metric, "status" | "value">): string | null {
  if (mode !== "sample") return null;
  if (m.status === "no_data" || m.value === null) return null;
  return app.results.sampleTag;
}
