"use client";

import { useState } from "react";
import { app } from "@/content/app";
import { ApiError, can, downloadExport, listExports, listQuarantine } from "@/lib/api";
import type { ExportInfo } from "@/lib/api";
import { Card, Notice, PageHead, formatDate } from "@/components/app/ui";
import { ghostBtn } from "@/components/app/buttons";
import { useData, useSession } from "@/components/app/session";
import { accountText, reasonText, sourceText, truncatedNotice } from "@/components/app/quarantine-rules";
import { Loaded } from "./loaded";

const f = app.files;

function Download({ info }: { info: ExportInfo }) {
  const [state, setState] = useState<"idle" | "busy" | "failed" | "missing">("idle");
  async function go() {
    setState("busy");
    try {
      const { blob, filename } = await downloadExport(info.kind);
      const url = URL.createObjectURL(blob);
      const link = Object.assign(document.createElement("a"), { href: url, download: filename });
      link.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
      setState("idle");
    } catch (e) {
      setState(e instanceof ApiError && e.status === 501 ? "missing" : "failed");
    }
  }
  return (
    <div className="grid gap-1">
      <button type="button" onClick={go} disabled={state === "busy"} aria-busy={state === "busy"} className={`${ghostBtn} w-fit`}>
        {state === "busy" ? f.downloading : f.download}
      </button>
      {state === "failed" && <p role="alert" className="text-[13.5px] text-accent">{f.downloadFailed}</p>}
      {state === "missing" && <p role="alert" className="text-[13.5px] text-white/70">{f.notBuilt}</p>}
    </div>
  );
}

export function Files() {
  const session = useSession();
  const allowed = can(session.role, "download");
  const state = useData(() => Promise.all([allowed ? listExports() : Promise.resolve(null), listQuarantine()]), `${session.tenantId}:${allowed}`);
  return (
    <>
      <PageHead eyebrow={f.eyebrow} title={f.title} body={f.body} />
      <Loaded state={state}>
        {([exports, list]) => {
          const quarantined = list.items;
          const notice = truncatedNotice(list);
          return (
          <>
            <Card className="p-5 sm:p-6">
              <h2 className="text-[20px] font-light">{f.exportsTitle}</h2>
              {!exports ? <p className="mt-3 text-[14.5px] text-white/70">{f.noRole}</p> : (
                <ul className="mt-3 divide-y divide-white/10 border-y border-white/10">
                  {exports.items.map((x) => (
                    <li key={x.kind} className="flex flex-wrap items-center justify-between gap-3 py-3">
                      <span className="text-[14.5px] text-white">
                        {f.exports[x.kind]}
                        {typeof x.rows === "number" && <span className="ml-2 font-mono text-[12px] text-white/60">{f.rows(x.rows)}</span>}
                      </span>
                      {x.available ? <Download info={x} /> : <span className="text-[13px] text-white/60">{f.unavailable}</span>}
                    </li>
                  ))}
                </ul>
              )}
            </Card>

            <h2 className="mb-2 mt-10 font-mono text-[12px] tracking-wide text-white/60">{f.quarantineTitle}</h2>
            <p className="mb-3 text-[14px] text-white/70">{f.quarantineBody}</p>
            {quarantined.length === 0 ? <Notice>{f.quarantineEmpty}</Notice> : (
              <ul className="divide-y divide-white/10 border-y border-white/10">
                {quarantined.map((q) => (
                  <li key={q.id} className="grid gap-1 py-4">
                    <span className="break-all font-mono text-[14px] text-white">{q.filename ?? q.id}</span>
                    <span className="text-[13.5px] text-white/70">
                      {formatDate(q.receivedAt)}{sourceText(q) && ` · ${sourceText(q)}`} · {accountText(q)}
                    </span>
                    <span className="text-[14px] text-accent">{reasonText(q)}</span>
                  </li>
                ))}
              </ul>
            )}
            {notice && <p className="mt-3 text-[14px] text-white/70">{notice}</p>}
          </>
          );
        }}
      </Loaded>
    </>
  );
}
