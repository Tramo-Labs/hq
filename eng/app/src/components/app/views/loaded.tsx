"use client";

import type { ReactNode } from "react";
import { app } from "@/content/app";
import { ApiError } from "@/lib/api";
import { Notice } from "@/components/app/ui";
import { ghostBtn } from "@/components/app/buttons";
import type { useData } from "@/components/app/session";

/** Renders the loading and error states of useData, and the children once ready. */
export function Loaded<T>({ state, children }: { state: ReturnType<typeof useData<T>>; children: (data: T) => ReactNode }) {
  if (state.status === "loading") return <Notice>{app.load.loading}</Notice>;
  if (state.status === "error") {
    const forbidden = state.error instanceof ApiError && state.error.status === 403;
    return (
      <Notice tone="alert">
        <p>{forbidden ? app.load.forbidden : app.load.failed}</p>
        {!forbidden && <button type="button" onClick={state.reload} className={`${ghostBtn} mt-3`}>{app.load.retry}</button>}
      </Notice>
    );
  }
  return <>{children(state.data)}</>;
}
