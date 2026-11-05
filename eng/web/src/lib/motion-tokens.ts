/** Tokens de movimiento (skill motion-foundations de ECC). Ningún componente define duraciones propias. */
export const motionTokens = {
  duration: { instant: 0.08, fast: 0.18, normal: 0.35, slow: 0.6, crawl: 1.0 },
  easing: { smooth: [0.22, 1, 0.36, 1] as const, sharp: [0.4, 0, 0.2, 1] as const },
  distance: { xs: 4, sm: 8, md: 16, lg: 24, xl: 48 },
  scale: { subtle: 0.98, press: 0.97, pop: 1.02 },
};
export const springs = {
  snappy: { type: "spring", stiffness: 300, damping: 30 } as const,
  gentle: { type: "spring", stiffness: 120, damping: 16 } as const,
};
