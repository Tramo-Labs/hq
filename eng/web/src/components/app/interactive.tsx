"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useState } from "react";
import { app } from "@/content/app";
import { decide, requestFullExport } from "@/lib/api";
import type { Outcome } from "@/lib/api";

import { ghostBtn, primaryBtn } from "@/components/app/buttons";

export function NavLinks({ items }: { items: { label: string; href: string }[] }) {
  const path = usePathname();
  return (
    <nav className="flex w-full flex-wrap gap-1 sm:w-auto">
      {items.map((n) => {
        const active = path === n.href;
        return (
          <Link key={n.href} href={n.href} aria-current={active ? "page" : undefined}
            className={`rounded-full px-3 py-2 text-[14px] ${active ? "bg-white/10 text-white" : "text-white/70 hover:text-white"}`}>
            {n.label}
          </Link>
        );
      })}
    </nav>
  );
}

type Variant = "client" | "operator";

/**
 * The three buttons of an exception (client) or a suggestion (operator). Each outcome has
 * its own Idempotency-Key, created once, so a double tap or a retry applies it only once.
 * "Es otra cosa" asks the client for a note first.
 */
export function Decision({ matchId, variant, by, hasProposal }: { matchId: string; variant: Variant; by: string; hasProposal: boolean }) {
  const copy = variant === "client" ? app.approvals : app.trays;
  const [keys] = useState<Record<Outcome, string>>(() => ({ approved: crypto.randomUUID(), rejected: crypto.randomUUID(), other: crypto.randomUUID() }));
  const [state, setState] = useState<"idle" | "note" | "saving" | "error">("idle");
  const [note, setNote] = useState("");
  const [done, setDone] = useState<{ outcome: Outcome; at: string } | null>(null);

  async function send(outcome: Outcome) {
    if (state === "saving") return;
    setState("saving");
    try {
      const res = await decide(matchId, { outcome, note: outcome === "other" && variant === "client" ? note.trim() : undefined }, keys[outcome], by);
      setDone({ outcome, at: res.decision.at });
    } catch {
      setState("error");
    }
  }

  if (done) {
    const time = new Date(done.at).toLocaleTimeString("es-AR", { hour: "2-digit", minute: "2-digit" });
    return (
      <p role="status" className="rounded-lg border border-white/15 px-3 py-2.5 text-[14px] text-white/85">
        <span className="text-white">{copy.decided[done.outcome]}</span> <span className="text-white/60">· {app.approvals.by(by, time)}</span>
      </p>
    );
  }

  if (state === "note") {
    return (
      <form className="grid gap-2" onSubmit={(e) => { e.preventDefault(); if (note.trim()) send("other"); }}>
        <label htmlFor={`note-${matchId}`} className="text-[14px] text-white/85">{app.approvals.noteLabel}</label>
        <textarea id={`note-${matchId}`} required rows={2} value={note} onChange={(e) => setNote(e.target.value)} placeholder={app.approvals.notePlaceholder}
          className="rounded-lg border border-white/20 bg-white/[0.04] px-3 py-2.5 text-[15px] placeholder:text-white/50 focus:border-white/50 focus:outline-none" />
        <div className="flex flex-wrap gap-2">
          <button type="submit" className={primaryBtn}>{app.approvals.send}</button>
          <button type="button" className={ghostBtn} onClick={() => setState("idle")}>{app.approvals.cancel}</button>
        </div>
      </form>
    );
  }

  const saving = state === "saving";
  return (
    <div className="grid gap-2">
      <div className="flex flex-wrap gap-2">
        {/* Approving registers the proposal, so there is nothing to approve without one. */}
        {hasProposal && <button type="button" disabled={saving} aria-busy={saving} className={primaryBtn} onClick={() => send("approved")}>{copy.actions.approved}</button>}
        <button type="button" disabled={saving} className={ghostBtn} onClick={() => send("rejected")}>{copy.actions.rejected}</button>
        <button type="button" disabled={saving} className={ghostBtn} onClick={() => (variant === "client" ? setState("note") : send("other"))}>{copy.actions.other}</button>
      </div>
      {state === "error" && <p role="alert" className="text-[14px] text-accent">{app.approvals.failed}</p>}
    </div>
  );
}

export function FullExport() {
  const [key] = useState(() => crypto.randomUUID());
  const [hours, setHours] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  return hours !== null ? (
    <p role="status" className="text-[14.5px] text-white/85">{app.files.full.queued(hours)}</p>
  ) : (
    <button type="button" disabled={busy} className={primaryBtn}
      onClick={async () => { setBusy(true); const r = await requestFullExport(key); setHours(r.expiresInHours); }}>
      {app.files.full.action}
    </button>
  );
}
