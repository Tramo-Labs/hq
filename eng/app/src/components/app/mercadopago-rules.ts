import { app } from "../../content/app";
import type { MercadoPagoConnection, Role } from "../../lib/api/types";

const m = app.mercadoPago;

/** What the Bancos step says about Mercado Pago. */
export type MpState = "unavailable" | "disconnected" | "connected" | "connected_no_import" | "needs_reconnect";

/**
 * Without the app's credentials nothing can connect, whatever the status says. Connected splits
 * on lastImportAt: absent means nothing was imported yet (the field is asked of backend-5).
 */
export function mpState(c: Pick<MercadoPagoConnection, "configured" | "status" | "lastImportAt">): MpState {
  if (!c.configured) return "unavailable";
  if (c.status === "needs_reconnect") return "needs_reconnect";
  if (c.status === "connected") return c.lastImportAt ? "connected" : "connected_no_import";
  return "disconnected";
}

/** The buttons a person sees. Only the owner connects, reconnects or disconnects (the core enforces it). */
export function mpActions(c: Pick<MercadoPagoConnection, "configured" | "status" | "lastImportAt">, role: Role) {
  const s = mpState(c);
  const owner = role === "owner";
  return {
    connect: owner && s === "disconnected",
    reconnect: owner && s === "needs_reconnect",
    disconnect: owner && (s === "connected" || s === "connected_no_import"),
  };
}

/**
 * One line about the last sync, only while connected: a sync stopped by quarantine or by Mercado
 * Pago not answering says so; imported says nothing (the import date already does), and an
 * unknown code nothing either, never Mercado Pago's text. Not connected: the state line covers it.
 */
export function syncText(c: Pick<MercadoPagoConnection, "configured" | "status" | "lastImportAt" | "lastSyncCode">): string | null {
  const s = mpState(c);
  if (s !== "connected" && s !== "connected_no_import") return null;
  if (c.lastSyncCode === "stopped_by_quarantine" || c.lastSyncCode === "mp_unavailable") return m.sync[c.lastSyncCode];
  return null;
}

/** A refused connect, in words. Never Mercado Pago's own text: an unknown code gets the generic one. */
export function mpErrorText(code: string): string {
  return (m.errors as Record<string, string>)[code] ?? m.errors.other;
}

/** What Mercado Pago put in the return URL. */
export type MpCallback = { kind: "connect"; code: string; state: string } | { kind: "cancelled" } | { kind: "missing" };

/**
 * Reads the return page's query. With an error Mercado Pago did not authorize (the person said no,
 * for instance): its text is never shown. Without code or state there is nothing to send.
 */
export function readCallback(search: string): MpCallback {
  const q = new URLSearchParams(search);
  if (q.has("error")) return { kind: "cancelled" };
  const code = q.get("code") ?? "";
  const state = q.get("state") ?? "";
  if (!code || !state) return { kind: "missing" };
  return { kind: "connect", code, state };
}
