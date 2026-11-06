import { app } from "../../content/app";
import type { PeriodRange, QuarantinedFile, QuarantineList } from "../../lib/api/types";

const f = app.files;

/** "2026-09-16" to its parts, without Date: a calendar day has no time zone to shift it. */
function parts(day: string) {
  const [y, m, d] = day.split("-").map(Number);
  return { y, m, d };
}

/**
 * A closed period in words: "del 16 al 30 de septiembre". The month goes once when both days
 * share it, and the year only shows when the two days are in different years.
 */
export function rangeText(r: PeriodRange): string {
  const a = parts(r.from);
  const b = parts(r.to);
  const sameYear = a.y === b.y;
  const to = f.day(b.d, f.months[b.m - 1], sameYear ? undefined : b.y);
  const from = sameYear && a.m === b.m ? String(a.d) : f.day(a.d, f.months[a.m - 1], sameYear ? undefined : a.y);
  return f.range(from, to);
}

/** Why a file did not enter, in words. period_closed names the range when the core sends it. */
export function reasonText(q: Pick<QuarantinedFile, "reason" | "closedPeriod">): string {
  if (q.reason === "period_closed" && q.closedPeriod) return f.periodClosed(rangeText(q.closedPeriod));
  return (q.reason && f.reasons[q.reason]) || f.otherReason;
}

/**
 * What the list says under its rows: when the core cut it at its 500 rows there are more files
 * that did not enter than the ones shown, and the view must say so instead of cutting in silence.
 */
export function truncatedNotice(list: Pick<QuarantineList, "truncated">): string | null {
  return list.truncated ? f.quarantineTruncated : null;
}

/** The account a file went to; a file that came by mail without one says so instead. */
export function accountText(q: Pick<QuarantinedFile, "accountName">): string {
  return q.accountName || f.noAccount;
}
