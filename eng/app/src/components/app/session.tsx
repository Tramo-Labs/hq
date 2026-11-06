"use client";

import { useRouter } from "next/navigation";
import { createContext, useCallback, useContext, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { endedElsewhere, getSession, logout, restore, subscribe, SessionExpired, type Session } from "@/lib/api";

type Area = "portal" | "console";

const Ctx = createContext<{ session: Session; signOut: () => Promise<void> } | null>(null);

function useGate() {
  const c = useContext(Ctx);
  if (!c) throw new Error("used outside SessionGate");
  return c;
}

/** The session of the screen. Only rendered under a ready gate, so never null there. */
export function useSession() {
  return useGate().session;
}

/** Logs out on the core, clears memory and goes to /entrar. */
export function useSignOut() {
  return useGate().signOut;
}

/** The in-memory session, or null. Null on the server, which never holds one. */
export function useCurrentSession() {
  return useSyncExternalStore(subscribe, getSession, () => null);
}

function back(path: string, reason?: "vencida" | "otra-pestana") {
  return `/entrar?volver=${encodeURIComponent(path)}${reason ? `&motivo=${reason}` : ""}`;
}

/**
 * Renders its children only with a session. After a reload the memory is empty, so it
 * asks for a new access token with the refresh cookie; without one, it goes to /entrar.
 * If the session ends later (refresh refused, logout), it goes to /entrar too.
 */
export function SessionGate({ area, loading, children }: { area: Area; loading: React.ReactNode; children: (s: Session) => React.ReactNode }) {
  const router = useRouter();
  const session = useCurrentSession();
  const had = useRef(false);
  const leaving = useRef(false);

  useEffect(() => {
    if (session) {
      had.current = true;
      return;
    }
    if (leaving.current) return;
    const here = window.location.pathname + window.location.search;
    // It had a session and lost it: another tab signed in as someone else or out (F33), or the
    // core refused the refresh. No second attempt either way.
    if (had.current) {
      router.replace(back(here, endedElsewhere() ? "otra-pestana" : "vencida"));
      return;
    }
    let gone = false;
    restore(area)
      .catch(() => null)
      .then((s) => {
        if (!gone && !s) router.replace(back(here));
      });
    return () => {
      gone = true;
    };
  }, [area, router, session]);

  const signOut = useCallback(async () => {
    leaving.current = true;
    try {
      await logout();
    } finally {
      router.replace("/entrar");
    }
  }, [router]);

  if (!session) return <>{loading}</>;
  return <Ctx.Provider value={{ session, signOut }}>{children(session)}</Ctx.Provider>;
}

type Load<T> = { status: "loading" } | { status: "ready"; data: T } | { status: "error"; error: unknown };

/**
 * Loads data for a screen once per key. A SessionExpired error is left to the gate, which
 * redirects as soon as the session is cleared; any other error is shown by the screen.
 */
export function useData<T>(load: () => Promise<T>, key: string): Load<T> & { reload: () => void } {
  const [state, setState] = useState<{ key: string; n: number; value: Load<T> }>({ key, n: 0, value: { status: "loading" } });
  const [n, setN] = useState(0);
  const reload = useCallback(() => setN((x) => x + 1), []);

  useEffect(() => {
    let gone = false;
    load().then(
      (data) => !gone && setState({ key, n, value: { status: "ready", data } }),
      (error) => !gone && !(error instanceof SessionExpired) && setState({ key, n, value: { status: "error", error } }),
    );
    return () => {
      gone = true;
    };
    // `load` is recreated on every render; the key says when the data changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, n]);

  const value: Load<T> = state.key === key && state.n === n ? state.value : { status: "loading" };
  return { ...value, reload };
}
