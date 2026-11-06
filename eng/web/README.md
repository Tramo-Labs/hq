# web

The landing, alone (D-21): one page (`/`) that reads no environment variable and calls no API.
Next.js 16 and Tailwind 4. Its copy is `src/content/site.ts`, owned by producto. The client
portal and the operator console are a separate app, `eng/app`, on its own domain; the landing
does not link to them.

The design tokens come from the shared package `eng/ui` (see its README). The fonts are loaded in
`src/app/layout.tsx`.

```bash
npm ci
npm run dev        # http://localhost:3001 (eng/app uses :3000)
npm run typecheck && npm run lint && npm run build
```

There are no tests here: everything that had them moved to `eng/app`.
