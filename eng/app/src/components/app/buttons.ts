/**
 * Button classes shared by server and client components. They live outside the
 * "use client" module because a server component cannot read plain values exported
 * from one: it gets a client reference, not the string.
 */
const btn = "inline-flex min-h-11 items-center justify-center rounded-full px-4 text-[14px] font-medium transition-transform active:scale-[0.97] disabled:opacity-60 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-white";
export const primaryBtn = `${btn} bg-paper text-ink hover:bg-white/90`;
export const ghostBtn = `${btn} border border-white/25 text-paper hover:bg-white/10`;
