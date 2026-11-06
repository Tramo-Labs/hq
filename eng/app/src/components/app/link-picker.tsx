"use client";

import { useEffect, useState } from "react";
import { app } from "@/content/app";
import { listOpenItems, type OpenItem, type OpenItems } from "@/lib/api";
import { ghostBtn, primaryBtn } from "@/components/app/buttons";
import { formatDate, formatMoney } from "@/components/app/ui";

type Load = { status: "loading" } | { status: "error" } | { status: "ready"; data: OpenItems };

/** "2026-11" -> "noviembre de 2026". */
const monthName = (ym: string) =>
  new Date(`${ym}-15T12:00:00Z`).toLocaleDateString("es-AR", { month: "long", year: "numeric", timeZone: "UTC" });

/**
 * "Vincular a mano": pick open items whose amounts add up exactly to the movement. The
 * direction follows the movement's sign (money in or out); the month starts as the
 * movement's and can widen to every month (an invoice is often paid the month after).
 * The sum and what is missing are shown as the person picks, and Vincular stays disabled
 * until it is exact.
 */
export function LinkPicker({ movement, saving, reload, onLink, onCancel }: {
  movement: { movementId: string; amount: number; date: string };
  saving: boolean;
  /** Bumped by the parent to fetch the list again (after item_taken), keeping the filter. */
  reload: number;
  onLink: (itemIds: string[]) => void;
  onCancel: () => void;
}) {
  const l = app.approvals.link;
  const direction = movement.amount >= 0 ? "in" : "out";
  const month = movement.date.slice(0, 7);
  const [allMonths, setAllMonths] = useState(false);
  const [result, setResult] = useState<{ key: string; load: Load } | null>(null);
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const key = `${direction}:${allMonths ? "all" : month}:${reload}`;
  // Loading is whatever the current filter has no answer for yet.
  const load: Load = result?.key === key ? result.load : { status: "loading" };

  useEffect(() => {
    let live = true;
    listOpenItems(direction, allMonths ? undefined : month)
      .then((data) => live && setResult({ key, load: { status: "ready", data } }))
      .catch(() => live && setResult({ key, load: { status: "error" } }));
    return () => { live = false; };
  }, [key, direction, month, allMonths]);

  const items: OpenItem[] = load.status === "ready" ? load.data.items : [];
  // Keep picks across the month filter; the sum counts only items still listed.
  const chosen = items.filter((i) => picked.has(i.id));
  const sum = chosen.reduce((t, i) => t + i.amount, 0);
  const left = movement.amount - sum;
  const exact = chosen.length > 0 && left === 0;
  const id = `link-${movement.movementId}`;

  function toggle(itemId: string) {
    setPicked((p) => {
      const next = new Set(p);
      if (next.has(itemId)) next.delete(itemId);
      else next.add(itemId);
      return next;
    });
  }

  return (
    <form className="grid gap-3" onSubmit={(e) => { e.preventDefault(); if (exact) onLink(chosen.map((i) => i.id)); }}>
      <fieldset className="grid gap-2" aria-describedby={`${id}-sum`}>
        <legend className="mb-1 text-[14px] text-white/85">{l.legend}</legend>
        <div className="flex flex-wrap gap-2" role="group" aria-label={l.period}>
          {[false, true].map((all) => (
            <button key={String(all)} type="button" aria-pressed={allMonths === all} onClick={() => setAllMonths(all)}
              className={`inline-flex min-h-11 items-center rounded-full border px-4 text-[14px] ${allMonths === all ? "border-white bg-white text-ink" : "border-white/25 text-white/80 hover:bg-white/10"}`}>
              {all ? l.allMonths : l.thisMonth(monthName(month))}
            </button>
          ))}
        </div>
        {load.status === "loading" && <p className="text-[14px] text-white/70">{l.loading}</p>}
        {load.status === "error" && <p role="alert" className="text-[14px] text-accent">{l.failed}</p>}
        {load.status === "ready" && items.length === 0 && <p className="text-[14px] text-white/70">{allMonths ? l.emptyAll : l.emptyMonth}</p>}
        {items.length > 0 && (
          <ul className="grid gap-2">
            {items.map((i) => (
              <li key={i.id}>
                <label className={`flex min-h-11 cursor-pointer items-start gap-3 rounded-lg border px-3 py-2.5 text-[14.5px] ${picked.has(i.id) ? "border-white/60 bg-white/[0.06]" : "border-white/15"}`}>
                  <input type="checkbox" checked={picked.has(i.id)} onChange={() => toggle(i.id)} className="mt-1 size-4 shrink-0 accent-white" />
                  <span className="grid min-w-0 flex-1 gap-0.5">
                    <span className="tabular-nums text-white">{formatMoney(i.amount)}</span>
                    <span className="break-words font-mono text-[12.5px] text-white/70">{i.refs.join(" · ") || l.noRef}</span>
                    <span className="text-[13px] text-white/60">{formatDate(i.date)}{i.party ? ` · ${app.onboarding.cuit(i.party)}` : ""}</span>
                  </span>
                </label>
              </li>
            ))}
          </ul>
        )}
        {load.status === "ready" && load.data.truncated && <p className="text-[13px] text-white/60">{l.truncated}</p>}
      </fieldset>

      <dl id={`${id}-sum`} aria-live="polite" className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 rounded-lg border border-white/10 px-3 py-2.5 text-[14px]">
        <dt className="text-white/60">{l.movement}</dt>
        <dd className="text-right tabular-nums text-white">{formatMoney(movement.amount)}</dd>
        <dt className="text-white/60">{l.picked(chosen.length)}</dt>
        <dd className="text-right tabular-nums text-white">{formatMoney(sum)}</dd>
        <dt className="text-white/60">{l.difference}</dt>
        <dd className={`text-right tabular-nums ${exact ? "text-white" : "text-accent"}`}>{exact ? l.exact : formatMoney(left)}</dd>
      </dl>

      <div className="flex flex-wrap gap-2">
        <button type="submit" disabled={!exact || saving} aria-busy={saving} className={primaryBtn}>{l.submit}</button>
        <button type="button" disabled={saving} className={ghostBtn} onClick={onCancel}>{app.approvals.cancel}</button>
      </div>
      {!exact && <p className="text-[13px] text-white/60">{l.hint}</p>}
    </form>
  );
}
