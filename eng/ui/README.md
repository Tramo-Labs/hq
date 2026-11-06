# @tramo/ui

What the landing (`eng/web`) and the app (`eng/app`, portal and console) share, so it lives in one
place (D-21). Each app depends on it as `"@tramo/ui": "file:../ui"`: npm links the folder, no
package is published and no third party dependency is added.

- `tokens.css`: the Tailwind 4 theme (colors and font stacks) and the base styles of `html`,
  `body`, headings and text. Each app imports it from its `globals.css` right after
  `@import "tailwindcss"`.
- `brand.ts`: the brand name.

Fonts are not here: `next/font` only works when it is called in the app's own code with literal
options, so each root layout loads DM Sans and Geist Mono into the variables `--font-dm-sans` and
`--font-geist-mono`, which `tokens.css` uses.

Next compiles this folder although it is outside the app: each `next.config.ts` lists
`@tramo/ui` in `transpilePackages` and sets `turbopack.root` and `outputFileTracingRoot` to `eng/`.
