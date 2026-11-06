"use client";

import { useState } from "react";
import { app } from "@/content/app";
import { ApiError, decideMatch, decideMovement } from "@/lib/api";
import type { MovementDecisionAction } from "@/lib/api";
import { ghostBtn, primaryBtn } from "@/components/app/buttons";

type Action = "approve" | "reject" | "escalate" | "other" | "resolve" | "return" | "revert";
/** Actions that open a note field first; the note is required for all of them. */
type NoteAction = "reject" | "other" | "resolve" | "return";

const actions: Action[] = ["approve", "reject", "escalate", "other", "resolve", "return", "revert"];
const keysFor = () => Object.fromEntries(actions.map((x) => [x, crypto.randomUUID()])) as Record<Action, string>;

function message(e: unknown) {
  return e instanceof ApiError ? app.approvals.errors[e.code] ?? (e.status === 403 ? app.approvals.errors.forbidden : app.approvals.failed) : app.approvals.failed;
}

/**
 * The buttons of one item. Each action has its own Idempotency-Key, created once and reused
 * if the same action is retried, so a double tap or a retry is applied once. After "Deshacer"
 * the keys are new: a fresh decision is a new action, not a retry of the first one.
 *
 * Buttons map 1:1 to the API's actions (qa F14); every note is required (qa F19, F4).
 * - client, Exceptions match (the match is the proposal): approve, reject (reason), other (note).
 * - client, Exceptions movement with no candidate or returned by the operator: other (note),
 *   link (disabled: no endpoint lists the open items to link yet).
 * - operator, Suggested match: approve, reject (reason), escalate.
 * - operator, movement the client called something else (client_other): resolve (note: what
 *   was done), return to the client (note: what is needed), link (disabled, as above).
 * "Deshacer" only appears after a decision made here, so each side reverts only its own row.
 */
export function Decision({ target, variant }: { target: { match: string } | { movement: string }; variant: "client" | "operator" }) {
  const a = app.approvals;
  const tr = app.trays;
  const labels: Record<string, string> = variant === "client" ? a.actions : tr.actions;
  const done: Record<string, string> = variant === "client" ? a.decided : tr.decided;
  const isMovement = "movement" in target;
  const [keys, setKeys] = useState(keysFor);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [noteFor, setNoteFor] = useState<NoteAction | null>(null);
  const [note, setNote] = useState("");
  const [decided, setDecided] = useState<{ action: Action; at: string } | null>(null);

  async function send(action: Action) {
    if (saving) return;
    setSaving(true);
    setError(null);
    try {
      const text = action === noteFor ? note.trim() || undefined : undefined;
      const res = isMovement
        ? await decideMovement(target.movement, { action: action as MovementDecisionAction, note: text }, keys[action])
        : await decideMatch(target.match, { action: action as "approve" | "reject" | "escalate" | "other" | "revert", note: text }, keys[action]);
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
            <span className="text-white">{done[decided.action]}</span> <span className="text-white/60">· {a.by(time)}</span>
          </p>
          <button type="button" disabled={saving} aria-busy={saving} className={ghostBtn} onClick={() => send("revert")}>{labels.revert}</button>
        </div>
        {err}
      </div>
    );
  }

  if (noteFor) {
    const id = `note-${isMovement ? target.movement : target.match}`;
    const form = {
      reject: { label: a.rejectLabel, placeholder: a.rejectPlaceholder, submit: labels.reject },
      other: { label: a.noteLabel, placeholder: a.notePlaceholder, submit: a.send },
      resolve: { label: tr.resolveLabel, placeholder: tr.resolvePlaceholder, submit: tr.actions.resolve },
      return: { label: tr.returnLabel, placeholder: tr.returnPlaceholder, submit: tr.actions.return },
    }[noteFor];
    return (
      <form className="grid gap-2" onSubmit={(e) => { e.preventDefault(); if (note.trim()) send(noteFor); }}>
        <label htmlFor={id} className="text-[14px] text-white/85">{form.label}</label>
        <textarea id={id} required maxLength={500} rows={2} value={note} onChange={(e) => setNote(e.target.value)} placeholder={form.placeholder}
          className="rounded-lg border border-white/20 bg-white/[0.04] px-3 py-2.5 text-[15px] placeholder:text-white/50 focus:border-white/50 focus:outline-none" />
        <div className="flex flex-wrap gap-2">
          <button type="submit" disabled={saving} aria-busy={saving} className={primaryBtn}>{form.submit}</button>
          <button type="button" disabled={saving} className={ghostBtn} onClick={() => { setNoteFor(null); setNote(""); setError(null); }}>{a.cancel}</button>
        </div>
        {err}
      </form>
    );
  }

  const link = isMovement && (
    <>
      <button type="button" disabled className={ghostBtn} aria-describedby={`link-${target.movement}`}>{a.actions.link}</button>
      <span id={`link-${target.movement}`} className="self-center text-[13px] text-white/60">{a.linkSoon}</span>
    </>
  );

  return (
    <div className="grid gap-2">
      <div className="flex flex-wrap gap-2">
        {isMovement && variant === "client" ? (
          <>
            <button type="button" className={ghostBtn} onClick={() => setNoteFor("other")}>{a.actions.other}</button>
            {link}
          </>
        ) : isMovement ? (
          <>
            <button type="button" className={primaryBtn} onClick={() => setNoteFor("resolve")}>{tr.actions.resolve}</button>
            <button type="button" className={ghostBtn} onClick={() => setNoteFor("return")}>{tr.actions.return}</button>
            {link}
          </>
        ) : variant === "client" ? (
          <>
            <button type="button" disabled={saving} aria-busy={saving} className={primaryBtn} onClick={() => send("approve")}>{a.actions.approve}</button>
            <button type="button" disabled={saving} className={ghostBtn} onClick={() => setNoteFor("reject")}>{a.actions.reject}</button>
            <button type="button" disabled={saving} className={ghostBtn} onClick={() => setNoteFor("other")}>{a.actions.other}</button>
          </>
        ) : (
          <>
            <button type="button" disabled={saving} aria-busy={saving} className={primaryBtn} onClick={() => send("approve")}>{tr.actions.approve}</button>
            <button type="button" disabled={saving} className={ghostBtn} onClick={() => setNoteFor("reject")}>{tr.actions.reject}</button>
            <button type="button" disabled={saving} className={ghostBtn} onClick={() => send("escalate")}>{tr.actions.escalate}</button>
          </>
        )}
      </div>
      {err}
    </div>
  );
}
