"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useState } from "react";
import { app } from "@/content/app";
import { decide } from "@/lib/api";
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
            className={`inline-flex min-h-11 items-center rounded-full px-3 text-[14px] ${active ? "bg-white/10 text-white" : "text-white/70 hover:text-white"}`}>
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
 * On the client side, "Rechazar" asks for an optional reason and "Es otra cosa" for a
 * required note; both go back to the operator with the decision.
 */
export function Decision({ matchId, variant, by, hasProposal }: { matchId: string; variant: Variant; by: string; hasProposal: boolean }) {
  const copy = variant === "client" ? app.approvals : app.trays;
  const a = app.approvals;
  const [keys] = useState<Record<Outcome, string>>(() => ({ approved: crypto.randomUUID(), rejected: crypto.randomUUID(), other: crypto.randomUUID() }));
  const [state, setState] = useState<"idle" | "saving" | "error">("idle");
  const [noteFor, setNoteFor] = useState<"rejected" | "other" | null>(null);
  const [note, setNote] = useState("");
  const [done, setDone] = useState<{ outcome: Outcome; at: string } | null>(null);

  async function send(outcome: Outcome) {
    if (state === "saving") return;
    setState("saving");
    try {
      const text = outcome === noteFor ? note.trim() : "";
      const res = await decide(matchId, { outcome, note: text || undefined }, keys[outcome], by);
      setDone({ outcome, at: res.decision.at });
    } catch {
      setState("error");
    }
  }

  if (done) {
    const time = new Date(done.at).toLocaleTimeString("es-AR", { hour: "2-digit", minute: "2-digit" });
    return (
      <p role="status" className="rounded-lg border border-white/15 px-3 py-2.5 text-[14px] text-white/85">
        <span className="text-white">{copy.decided[done.outcome]}</span> <span className="text-white/60">· {a.by(by, time)}</span>
      </p>
    );
  }

  const saving = state === "saving";

  if (noteFor) {
    const required = noteFor === "other";
    const id = `note-${matchId}`;
    return (
      <form className="grid gap-2" onSubmit={(e) => { e.preventDefault(); if (!required || note.trim()) send(noteFor); }}>
        <label htmlFor={id} className="text-[14px] text-white/85">
          {required ? a.noteLabel : a.rejectLabel}
          {!required && <span className="ml-2 font-mono text-[12px] text-white/60">{a.optional}</span>}
        </label>
        <textarea id={id} required={required} rows={2} value={note} onChange={(e) => setNote(e.target.value)} placeholder={required ? a.notePlaceholder : a.rejectPlaceholder}
          className="rounded-lg border border-white/20 bg-white/[0.04] px-3 py-2.5 text-[15px] placeholder:text-white/50 focus:border-white/50 focus:outline-none" />
        <div className="flex flex-wrap gap-2">
          <button type="submit" disabled={saving} aria-busy={saving} className={primaryBtn}>{required ? a.send : a.actions.rejected}</button>
          <button type="button" disabled={saving} className={ghostBtn} onClick={() => { setNoteFor(null); setNote(""); setState("idle"); }}>{a.cancel}</button>
        </div>
        {state === "error" && <p role="alert" className="text-[14px] text-accent">{a.failed}</p>}
      </form>
    );
  }

  const client = variant === "client";
  return (
    <div className="grid gap-2">
      <div className="flex flex-wrap gap-2">
        {/* Approving registers the proposal, so there is nothing to approve without one. */}
        {hasProposal && <button type="button" disabled={saving} aria-busy={saving} className={primaryBtn} onClick={() => send("approved")}>{copy.actions.approved}</button>}
        <button type="button" disabled={saving} className={ghostBtn} onClick={() => (client ? setNoteFor("rejected") : send("rejected"))}>{copy.actions.rejected}</button>
        <button type="button" disabled={saving} className={ghostBtn} onClick={() => (client ? setNoteFor("other") : send("other"))}>{copy.actions.other}</button>
      </div>
      {state === "error" && <p role="alert" className="text-[14px] text-accent">{a.failed}</p>}
    </div>
  );
}
