"use client";

import { useState } from "react";
import { app } from "@/content/app";
import { ApiError, decideMatch, decideMovement } from "@/lib/api";
import { ghostBtn, primaryBtn } from "@/components/app/buttons";

type Action = "approve" | "reject" | "escalate" | "other" | "revert";

const keysFor = () => ({ approve: crypto.randomUUID(), reject: crypto.randomUUID(), escalate: crypto.randomUUID(), other: crypto.randomUUID(), revert: crypto.randomUUID() });

function message(e: unknown) {
  return e instanceof ApiError ? app.approvals.errors[e.code] ?? (e.status === 403 ? app.approvals.errors.forbidden : app.approvals.failed) : app.approvals.failed;
}

/**
 * The buttons of one item. Each action has its own Idempotency-Key, created once and reused
 * if the same action is retried, so a double tap or a retry is applied once. After "Deshacer"
 * the keys are new: a fresh decision is a new action, not a retry of the first one.
 *
 * Buttons map 1:1 to the API's actions (qa F14); a reason is required to reject (qa F19).
 * - client, Exceptions match (the match is the proposal): approve, reject (reason), other (note).
 * - client, Exceptions movement with no candidate: other (note), link (shown disabled: no
 *   endpoint lists the open items to link yet), revert after deciding.
 * - operator, Suggested match: approve, reject (reason), escalate.
 */
export function Decision({ target, variant }: { target: { match: string } | { movement: string }; variant: "client" | "operator" }) {
  const a = app.approvals;
  const labels = variant === "client" ? a.actions : app.trays.actions;
  const done = variant === "client" ? a.decided : app.trays.decided;
  const isMovement = "movement" in target;
  const [keys, setKeys] = useState(keysFor);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [noteFor, setNoteFor] = useState<"reject" | "other" | null>(null);
  const [note, setNote] = useState("");
  const [decided, setDecided] = useState<{ action: Action; at: string } | null>(null);

  async function send(action: Action) {
    if (saving) return;
    setSaving(true);
    setError(null);
    try {
      const text = action === noteFor ? note.trim() || undefined : undefined;
      const res = isMovement
        ? await decideMovement(target.movement, { action: action as "other" | "revert", note: text }, keys[action])
        : await decideMatch(target.match, { action, note: text }, keys[action]);
      if (action === "revert") {
        setDecided(null);
        setKeys(keysFor());
        setNote("");
      } else {
        setDecided({ action, at: res.at });
      }
      setNoteFor(null);
    } catch (e) {
      setError(message(e));
    } finally {
      setSaving(false);
    }
  }

  const err = error && <p role="alert" className="text-[14px] text-accent">{error}</p>;

  if (decided) {
    const time = new Date(decided.at).toLocaleTimeString("es-AR", { hour: "2-digit", minute: "2-digit", hour12: false, timeZone: "America/Argentina/Buenos_Aires" });
    return (
      <div className="grid gap-2">
        <div className="flex flex-wrap items-center gap-3">
          <p role="status" className="text-[14px] text-white/85">
            <span className="text-white">{(done as Record<string, string>)[decided.action]}</span> <span className="text-white/60">· {a.by(time)}</span>
          </p>
          <button type="button" disabled={saving} aria-busy={saving} className={ghostBtn} onClick={() => send("revert")}>{labels.revert}</button>
        </div>
        {err}
      </div>
    );
  }

  if (noteFor) {
    const other = noteFor === "other";
    const id = `note-${isMovement ? target.movement : target.match}`;
    return (
      <form className="grid gap-2" onSubmit={(e) => { e.preventDefault(); if (note.trim()) send(noteFor); }}>
        <label htmlFor={id} className="text-[14px] text-white/85">{other ? a.noteLabel : a.rejectLabel}</label>
        <textarea id={id} required maxLength={500} rows={2} value={note} onChange={(e) => setNote(e.target.value)} placeholder={other ? a.notePlaceholder : a.rejectPlaceholder}
          className="rounded-lg border border-white/20 bg-white/[0.04] px-3 py-2.5 text-[15px] placeholder:text-white/50 focus:border-white/50 focus:outline-none" />
        <div className="flex flex-wrap gap-2">
          <button type="submit" disabled={saving} aria-busy={saving} className={primaryBtn}>{other ? a.send : labels.reject}</button>
          <button type="button" disabled={saving} className={ghostBtn} onClick={() => { setNoteFor(null); setNote(""); setError(null); }}>{a.cancel}</button>
        </div>
        {err}
      </form>
    );
  }

  return (
    <div className="grid gap-2">
      <div className="flex flex-wrap gap-2">
        {isMovement ? (
          <>
            <button type="button" className={ghostBtn} onClick={() => setNoteFor("other")}>{a.actions.other}</button>
            <button type="button" disabled className={ghostBtn} aria-describedby={`link-${target.movement}`}>{a.actions.link}</button>
            <span id={`link-${target.movement}`} className="self-center text-[13px] text-white/60">{a.linkSoon}</span>
          </>
        ) : variant === "client" ? (
          <>
            <button type="button" disabled={saving} aria-busy={saving} className={primaryBtn} onClick={() => send("approve")}>{a.actions.approve}</button>
            <button type="button" disabled={saving} className={ghostBtn} onClick={() => setNoteFor("reject")}>{a.actions.reject}</button>
            <button type="button" disabled={saving} className={ghostBtn} onClick={() => setNoteFor("other")}>{a.actions.other}</button>
          </>
        ) : (
          <>
            <button type="button" disabled={saving} aria-busy={saving} className={primaryBtn} onClick={() => send("approve")}>{app.trays.actions.approve}</button>
            <button type="button" disabled={saving} className={ghostBtn} onClick={() => setNoteFor("reject")}>{app.trays.actions.reject}</button>
            <button type="button" disabled={saving} className={ghostBtn} onClick={() => send("escalate")}>{app.trays.actions.escalate}</button>
          </>
        )}
      </div>
      {err}
    </div>
  );
}
