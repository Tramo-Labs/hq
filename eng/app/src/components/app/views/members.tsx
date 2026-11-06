"use client";

import { useId, useState } from "react";
import { app } from "@/content/app";
import { addMember, listMembers, removeMember, type Member, type Role } from "@/lib/api";
import { Card, Notice, PageHead } from "@/components/app/ui";
import { ghostBtn, primaryBtn } from "@/components/app/buttons";
import { useData, useSession, useSignOut } from "@/components/app/session";
import { byStatus, grantableRoles, managesMembers, memberProblem, removeWording } from "@/components/app/members-rules";
import { Loaded } from "./loaded";

const t = app.members;
const field = "min-h-11 w-full rounded-lg border border-white/20 bg-white/[0.04] px-3 text-[16px] text-white placeholder:text-white/50 focus:border-white/50 focus:outline-none";

/** /app/miembros: the owner sees who is in and who is invited, invites, and removes (spec §1, D-19). */
export function Members() {
  const session = useSession();
  const allowed = managesMembers(session.role);
  const state = useData(() => (allowed ? listMembers() : Promise.resolve(null)), `${session.tenantId}:${allowed}`);
  const [notice, setNotice] = useState<string | null>(null);
  if (!allowed) {
    return (
      <>
        <PageHead eyebrow={t.eyebrow} title={t.title} />
        <Notice>{t.noRole}</Notice>
      </>
    );
  }
  // Reloading the list remounts what is inside Loaded, so the invite form (and its message) and
  // the notice about a row that was already gone live outside it.
  const gone = () => { setNotice(t.problems.gone); state.reload(); };
  return (
    <>
      <PageHead eyebrow={t.eyebrow} title={t.title} body={t.body} />
      <div className="mb-6">
        <Invite roles={grantableRoles(session.role)} onDone={() => { setNotice(null); state.reload(); }} />
      </div>
      {notice && <p role="status" className="mb-4 text-[14.5px] text-white/85">{notice}</p>}
      <Loaded state={state}>
        {(page) => {
          const { active, pending } = byStatus(page?.items ?? []);
          return (
            <div className="grid gap-6">
              <Card className="p-5 sm:p-6">
                <h2 className="text-[20px] font-light">{t.activeTitle}</h2>
                <ul className="mt-3 divide-y divide-white/10 border-y border-white/10">
                  {active.map((m) => <Row key={m.userId} m={m} self={m.email === session.email} onDone={() => { setNotice(null); state.reload(); }} onGone={gone} />)}
                </ul>
              </Card>
              <Card className="p-5 sm:p-6">
                <h2 className="text-[20px] font-light">{t.pendingTitle}</h2>
                <p className="mt-2 text-[14px] text-white/60">{t.pendingNote}</p>
                {pending.length === 0 ? <p className="mt-3 text-[14.5px] text-white/70">{t.noPending}</p> : (
                  <ul className="mt-3 divide-y divide-white/10 border-y border-white/10">
                    {pending.map((m) => <Row key={m.userId} m={m} self={false} onDone={() => { setNotice(null); state.reload(); }} onGone={gone} />)}
                  </ul>
                )}
              </Card>
            </div>
          );
        }}
      </Loaded>
    </>
  );
}

function Invite({ roles, onDone }: { roles: Role[]; onDone: () => void }) {
  const ids = { email: useId(), role: useId(), help: useId(), msg: useId() };
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<Role>(roles.includes("viewer") ? "viewer" : roles[0]);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  // One key per invitation the person means to send: a retry after a lost answer is the same one.
  const [key, setKey] = useState(() => crypto.randomUUID());

  async function send(e: React.FormEvent) {
    e.preventDefault();
    if (busy) return;
    setBusy(true);
    setMsg(null);
    try {
      const m = await addMember(email.trim(), role, key);
      setMsg({ ok: true, text: t.invited(m.email) });
      setEmail("");
      setKey(crypto.randomUUID());
      onDone();
    } catch (err) {
      setMsg({ ok: false, text: t.problems[memberProblem(err)] });
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card className="p-5 sm:p-6">
      <h2 className="text-[20px] font-light">{t.inviteTitle}</h2>
      <form onSubmit={send} aria-describedby={msg ? ids.msg : undefined} className="mt-4 grid gap-4 sm:grid-cols-[1fr_auto] sm:items-end">
        <div className="grid gap-1.5">
          <label htmlFor={ids.email} className="text-[14px] text-white/85">{t.email}</label>
          <input id={ids.email} type="email" name="email" autoComplete="off" inputMode="email" required maxLength={254}
            value={email} onChange={(e) => { setEmail(e.target.value); setMsg(null); setKey(crypto.randomUUID()); }} className={field} />
        </div>
        <div className="grid gap-1.5">
          <label htmlFor={ids.role} className="text-[14px] text-white/85">{t.role}</label>
          <select id={ids.role} value={role} aria-describedby={ids.help} onChange={(e) => { setRole(e.target.value as Role); setKey(crypto.randomUUID()); }}
            className={`${field} sm:w-48`}>
            {roles.map((r) => <option key={r} value={r} className="bg-ink text-white">{app.roles[r]}</option>)}
          </select>
        </div>
        <p id={ids.help} className="text-[13px] text-white/60 sm:col-span-2">{t.roleHelp[role]}</p>
        {msg && <p id={ids.msg} role={msg.ok ? "status" : "alert"} className={`break-words text-[14.5px] sm:col-span-2 ${msg.ok ? "text-white/85" : "text-accent"}`}>{msg.text}</p>}
        <button type="submit" disabled={busy} aria-busy={busy} className={`${primaryBtn} w-fit`}>{busy ? t.inviting : t.invite}</button>
      </form>
    </Card>
  );
}

function Row({ m, self, onDone, onGone }: { m: Member; self: boolean; onDone: () => void; onGone: () => void }) {
  const signOut = useSignOut();
  const [asking, setAsking] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const who = m.name ?? m.email;
  const pending = m.status === "pending";
  const words = removeWording(m.status);

  async function remove() {
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      await removeMember(m.userId);
      if (self) return signOut();
      onDone();
    } catch (err) {
      const p = memberProblem(err);
      if (p === "gone") return onGone();
      setError(t.problems[p]);
    } finally {
      setBusy(false);
      setAsking(false);
    }
  }

  return (
    <li className="grid gap-2 py-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="min-w-0">
          <p className="break-all text-[14.5px] text-white">
            {who}
            {self && <span className="ml-2 text-[13px] text-white/60">({t.you})</span>}
          </p>
          {m.name && <p className="break-all text-[13px] text-white/60">{m.email}</p>}
        </div>
        <div className="flex items-center gap-2">
          <span className="font-mono text-[12px] text-white/70">{app.roles[m.role]}</span>
          {pending && <span className="rounded-full border border-white/25 px-2 py-0.5 font-mono text-[11.5px] text-white/80">{t.pendingBadge}</span>}
          {!asking && <button type="button" onClick={() => { setAsking(true); setError(null); }} className={ghostBtn}>{t[words.open]}</button>}
        </div>
      </div>
      {asking && (
        <div role="group" aria-label={t[words.open]} className="grid gap-2 rounded-lg border border-white/15 p-3">
          <p className="break-words text-[14px] text-white/85">{self ? t.confirmSelf : pending ? t.confirmPending(who) : t.confirmActive(who)}</p>
          <div className="flex flex-wrap gap-2">
            <button type="button" onClick={remove} disabled={busy} aria-busy={busy} className={primaryBtn}>{busy ? t.removing : t[words.confirm]}</button>
            <button type="button" onClick={() => setAsking(false)} disabled={busy} className={ghostBtn}>{t.keep}</button>
          </div>
        </div>
      )}
      {error && <p role="alert" className="text-[14px] text-accent">{error}</p>}
    </li>
  );
}
