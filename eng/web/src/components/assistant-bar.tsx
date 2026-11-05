"use client";
import { useEffect, useState } from "react";
import { motion, useReducedMotion } from "motion/react";
import { site } from "@/content/site";
import { springs } from "@/lib/motion-tokens";

/** Barra flotante al pie, como la de DANKA: una pregunta que se escribe sola y lleva al formulario con el texto cargado. */
export function AssistantBar() {
  const prompts = site.assistant.prompts;
  const reduced = useReducedMotion();
  const [i, setI] = useState(0);
  const [typed, setTyped] = useState("");
  const [value, setValue] = useState("");
  const [visible, setVisible] = useState(false);

  useEffect(() => {
    const onScroll = () => setVisible(window.scrollY > 500 && window.innerHeight + window.scrollY < document.body.scrollHeight - 700);
    onScroll(); window.addEventListener("scroll", onScroll, { passive: true });
    return () => window.removeEventListener("scroll", onScroll);
  }, []);

  useEffect(() => {
    if (reduced) return;
    const full = prompts[i]; let k = 0;
    const t = setInterval(() => {
      k++; setTyped(full.slice(0, k));
      if (k >= full.length) { clearInterval(t); setTimeout(() => setI((n) => (n + 1) % prompts.length), 2600); }
    }, 38);
    return () => clearInterval(t);
  }, [i, prompts, reduced]);
  const shown = reduced ? prompts[i] : typed;

  const go = (e: React.FormEvent) => {
    e.preventDefault();
    const ta = document.querySelector<HTMLTextAreaElement>("#contacto textarea");
    if (ta) ta.value = value || shown;
    document.querySelector("#contacto")?.scrollIntoView({ behavior: reduced ? "auto" : "smooth" });
    ta?.focus({ preventScroll: true });
  };

  return (
    <motion.form onSubmit={go} aria-label={site.assistant.label}
      initial={false} animate={{ y: visible ? 0 : 120, opacity: visible ? 1 : 0 }} transition={springs.gentle}
      className="fixed inset-x-4 bottom-4 z-30 mx-auto flex max-w-2xl items-center gap-3 rounded-full border border-white/15 bg-ink-2/90 py-2 pl-5 pr-2 shadow-[0_20px_60px_-20px_rgba(0,0,0,0.9)] backdrop-blur">
      <span className="hidden size-2 shrink-0 rounded-full bg-accent sm:block" aria-hidden />
      <input value={value} onChange={(e) => setValue(e.target.value)} placeholder={shown + (reduced ? "" : "▍")}
        className="min-w-0 flex-1 bg-transparent font-mono text-[13.5px] text-white placeholder:text-white/50 focus:outline-none" />
      <button type="submit" className="shrink-0 rounded-full bg-paper px-4 py-2 text-[13px] font-medium text-ink">{site.assistant.cta}</button>
    </motion.form>
  );
}
