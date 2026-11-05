"use client";
import { useEffect, useRef } from "react";
import { useReducedMotion } from "motion/react";

/** Fondo del hero: partículas que fluyen de izquierda a derecha como un flujo de datos. Canvas liviano; se apaga con reduced motion. */
export function Particles({ className = "" }: { className?: string }) {
  const ref = useRef<HTMLCanvasElement>(null);
  const reduced = useReducedMotion();
  useEffect(() => {
    if (reduced) return;
    const c = ref.current; if (!c) return;
    const ctx = c.getContext("2d"); if (!ctx) return;
    let w = 0, h = 0, raf = 0;
    const N = 140;
    const ps = Array.from({ length: N }, () => ({ x: Math.random(), y: Math.random(), v: 0.0004 + Math.random() * 0.0012, r: 0.6 + Math.random() * 1.2, a: 0.15 + Math.random() * 0.45 }));
    const resize = () => { const d = Math.min(2, window.devicePixelRatio || 1); w = c.clientWidth; h = c.clientHeight; c.width = w * d; c.height = h * d; ctx.setTransform(d, 0, 0, d, 0, 0); };
    resize(); window.addEventListener("resize", resize);
    const tick = () => {
      ctx.clearRect(0, 0, w, h);
      for (const p of ps) {
        p.x += p.v; if (p.x > 1.02) { p.x = -0.02; p.y = Math.random(); }
        const y = p.y * h + Math.sin(p.x * 9 + p.y * 6) * 12;
        ctx.beginPath(); ctx.arc(p.x * w, y, p.r, 0, Math.PI * 2);
        ctx.fillStyle = `rgba(245,179,66,${p.a * (0.4 + 0.6 * p.x)})`; ctx.fill();
      }
      raf = requestAnimationFrame(tick);
    };
    raf = requestAnimationFrame(tick);
    return () => { cancelAnimationFrame(raf); window.removeEventListener("resize", resize); };
  }, [reduced]);
  return <canvas ref={ref} aria-hidden className={`pointer-events-none absolute inset-0 -z-10 h-full w-full ${className}`} />;
}
