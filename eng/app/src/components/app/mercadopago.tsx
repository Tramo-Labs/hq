"use client";

import Link from "next/link";
import { useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { app } from "@/content/app";
import { ApiError, authorizeMercadoPago, connectMercadoPago, disconnectMercadoPago, getMercadoPago } from "@/lib/api";
import type { MercadoPagoConnection } from "@/lib/api";
import { Notice, PageHead, formatDate } from "@/components/app/ui";
import { ghostBtn, primaryBtn } from "@/components/app/buttons";
import { useData, useSession } from "@/components/app/session";
import { mpActions, mpErrorText, mpState, readCallback, syncText } from "@/components/app/mercadopago-rules";

const m = app.mercadoPago;

/**
 * Mercado Pago inside the Bancos step. Roles the core does not show it to (403) see nothing;
 * a failed load says so in one line instead of taking the whole step.
 */
export function MercadoPagoBlock() {
  const session = useSession();
  const state = useData(() => getMercadoPago().catch((e) => {
    if (e instanceof ApiError && e.status === 403) return null;
    throw e;
  }), session.tenantId);
  if (state.status === "loading") return null;
  if (state.status === "error") return <p className="mt-4 text-[14px] text-white/70">{app.load.failed}</p>;
  if (!state.data) return null;
  return <Connection initial={state.data} />;
}

function Connection({ initial }: { initial: MercadoPagoConnection }) {
  const session = useSession();
  const router = useRouter();
  const [conn, setConn] = useState(initial);
  const [busy, setBusy] = useState<"open" | "disconnect" | null>(null);
  const [asking, setAsking] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [key, setKey] = useState(() => crypto.randomUUID());

  const s = mpState(conn);
  const can = mpActions(conn, session.role);

  async function open() {
    setBusy("open");
    setError(null);
    try {
      const { authorizeUrl } = await authorizeMercadoPago(key);
      // Sample mode answers with this portal's own return page: stay in the app, keep the memory.
      if (authorizeUrl.startsWith("/")) router.push(authorizeUrl);
      else window.location.assign(authorizeUrl);
    } catch (e) {
      if (e instanceof ApiError && e.code === "not_configured") setConn({ ...conn, configured: false });
      else setError(e instanceof ApiError && e.status === 403 ? m.ownerOnly : m.failed);
      setKey(crypto.randomUUID());
      setBusy(null);
    }
  }

  async function disconnect() {
    setBusy("disconnect");
    setError(null);
    try {
      setConn(await disconnectMercadoPago());
      setAsking(false);
    } catch (e) {
      setError(e instanceof ApiError && e.status === 403 ? m.ownerOnly : m.failed);
    } finally {
      setBusy(null);
    }
  }

  return (
    <section aria-labelledby="mp-title" className="mt-6">
      <h3 id="mp-title" className="text-[16px] text-white">{m.title}</h3>
      <p className="mt-1 text-[14px] leading-relaxed text-white/70">{m.body}</p>
      <p className="mt-3 text-[14.5px] text-white">{m.states[s]}</p>
      {(s === "connected" || s === "connected_no_import") && conn.connectedAt && (
        <p className="mt-1 text-[13.5px] text-white/70">
          {m.connectedOn(formatDate(conn.connectedAt))}
          {conn.lastImportAt && ` · ${m.lastImport(formatDate(conn.lastImportAt))}`}
        </p>
      )}
      {syncText(conn) && <p className="mt-1 text-[13.5px] text-accent">{syncText(conn)}</p>}
      {conn.revokeInMercadoPago && <div className="mt-3"><Notice>{m.revoke}</Notice></div>}

      {(can.connect || can.reconnect) && (
        <div className="mt-4">
          <button type="button" onClick={open} disabled={busy !== null} aria-busy={busy === "open"} className={primaryBtn}>
            {busy === "open" ? m.opening : can.reconnect ? m.reconnect : m.connect}
          </button>
        </div>
      )}
      {can.disconnect && !asking && (
        <div className="mt-4">
          <button type="button" onClick={() => setAsking(true)} className={ghostBtn}>{m.disconnect}</button>
        </div>
      )}
      {can.disconnect && asking && (
        <div className="mt-4 grid gap-3">
          <p className="text-[14px] text-white/85">{m.confirm}</p>
          <div className="flex flex-wrap gap-2">
            <button type="button" onClick={disconnect} disabled={busy !== null} aria-busy={busy === "disconnect"} className={primaryBtn}>
              {busy === "disconnect" ? m.disconnecting : m.confirmYes}
            </button>
            <button type="button" onClick={() => setAsking(false)} disabled={busy !== null} className={ghostBtn}>{m.cancel}</button>
          </div>
        </div>
      )}
      {s !== "unavailable" && session.role !== "owner" && <p className="mt-3 text-[13px] text-white/60">{m.ownerOnly}</p>}
      <p role="alert" className="mt-3 text-[14px] text-accent empty:hidden">{error}</p>
    </section>
  );
}

/**
 * The page Mercado Pago sends the browser back to. It reads code and state once, takes them out
 * of the address bar (a reload must not send them again), and asks the core to finish.
 */
export function MercadoPagoReturn() {
  const [result, setResult] = useState<{ ok: true } | { ok: false; text: string } | null>(null);
  const started = useRef(false);

  useEffect(() => {
    if (started.current) return;
    started.current = true;
    const cb = readCallback(window.location.search);
    window.history.replaceState(null, "", window.location.pathname);
    const done: Promise<{ ok: true } | { ok: false; text: string }> = cb.kind === "connect"
      ? connectMercadoPago(cb.code, cb.state, crypto.randomUUID()).then(
        () => ({ ok: true as const }),
        (e) => ({ ok: false as const, text: mpErrorText(e instanceof ApiError ? e.code : "other") }),
      )
      : Promise.resolve({ ok: false as const, text: mpErrorText(cb.kind) });
    done.then(setResult);
  }, []);

  return (
    <>
      <PageHead eyebrow={m.back.eyebrow} title={m.back.title} />
      {!result ? <Notice>{m.back.connecting}</Notice> : <Notice tone={result.ok ? "plain" : "alert"}>{result.ok ? m.back.done : result.text}</Notice>}
      {result && (
        <Link href="/app" className={`${ghostBtn} mt-6`}>{m.back.toOnboarding}</Link>
      )}
    </>
  );
}
