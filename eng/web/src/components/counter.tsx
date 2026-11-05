"use client";
import { useEffect, useRef, useState } from "react";
import { useInView, useReducedMotion } from "motion/react";
import { motionTokens } from "@/lib/motion-tokens";

/** Cuenta de 0 al valor cuando entra en pantalla. Acepta "1.284", "91 %", "19,5". */
export function Counter({ value, className }: { value: string; className?: string }) {
  const ref = useRef<HTMLSpanElement>(null);
  const inView = useInView(ref, { once: true, margin: "-40px" });
  const reduced = useReducedMotion();
  const [shown, setShown] = useState(value);
  useEffect(() => {
    if (!inView || reduced) return;
    const m = value.match(/^([\d.,]+)(.*)$/);
    if (!m) return;
    const raw = m[1]; const suffix = m[2];
    const decimals = raw.includes(",") ? raw.split(",")[1].length : 0;
    const target = parseFloat(raw.replace(/\./g, "").replace(",", "."));
    const start = performance.now(); const ms = motionTokens.duration.crawl * 1000;
    let frame = 0;
    const tick = (now: number) => {
      const t = Math.min(1, (now - start) / ms); const e = 1 - Math.pow(1 - t, 3);
      const n = target * e;
      const s = decimals ? n.toFixed(decimals).replace(".", ",") : Math.round(n).toLocaleString("es-AR");
      setShown(s + suffix);
      if (t < 1) frame = requestAnimationFrame(tick);
    };
    frame = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(frame);
  }, [inView, reduced, value]);
  return <span ref={ref} className={className}>{shown}</span>;
}
