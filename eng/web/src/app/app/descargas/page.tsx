import type { Metadata } from "next";
import { app } from "@/content/app";
import { Card, PageHead, formatDate } from "@/components/app/ui";
import { FullExport } from "@/components/app/interactive";
import { can, getSession, listFiles } from "@/lib/api";

export const metadata: Metadata = { title: app.files.eyebrow };

const tone = { ready: "text-white/75", problem: "text-accent", duplicate: "text-white/75", quarantine: "text-accent" } as const;

export default async function Files() {
  const f = app.files;
  const [session, { items }] = await Promise.all([getSession("portal"), listFiles()]);
  return (
    <>
      <PageHead eyebrow={f.eyebrow} title={f.title} body={f.body} />

      <Card className="p-5 sm:p-6">
        <h2 className="text-[20px] font-light">{f.full.name}</h2>
        <p className="mt-2 max-w-2xl text-[15px] leading-relaxed text-white/70">{f.full.body}</p>
        <div className="mt-5">{can(session.role, "download") ? <FullExport /> : <p className="text-[14.5px] text-white/70">{f.full.noRole}</p>}</div>
      </Card>

      <h2 className="mb-3 mt-10 font-mono text-[12px] tracking-wide text-white/60">{f.listTitle}</h2>
      <ul className="divide-y divide-white/10 border-y border-white/10">
        {items.map((file, i) => (
          <li key={`${file.name}-${i}`} className="grid gap-1 py-4 sm:grid-cols-[minmax(0,2fr)_6rem_minmax(0,1fr)_minmax(0,1.4fr)] sm:items-baseline sm:gap-4">
            <span className="break-all font-mono text-[14px] text-white">{file.name}</span>
            <span className="text-[13.5px] text-white/70"><span className="sr-only">{f.columns.when}: </span>{formatDate(file.uploadedAt)}</span>
            <span className="text-[13.5px] text-white/70"><span className="sr-only">{f.columns.by}: </span>{file.by}</span>
            <span className={`text-[13.5px] ${tone[file.status]}`}><span className="sr-only">{f.columns.status}: </span>{f.status[file.status]}</span>
          </li>
        ))}
      </ul>
    </>
  );
}
