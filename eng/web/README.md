# web

Landing (`/`), client portal (`/app`), operator console (`/operador`) and login (`/entrar`).
Next.js 16 and Tailwind 4. Copy lives in `src/content/` (`site.ts` and `app.ts`, owned by producto).

## Two modes

| `CORE_API_URL` | Mode | What the portal and the console read |
|---|---|---|
| unset | sample | Synthetic fixtures in the spec's shapes (`src/lib/api/fixtures.ts`), "Datos de ejemplo" banner. Sample logins are listed on `/entrar`. |
| `http://host:port` | live | The core, through a same-origin rewrite of `/v1/*` (`next.config.ts`). No banner. |

The mode and the rewrite are compiled into the build: set `CORE_API_URL` for `npm run build`
and keep it for `npm run start`. Only the mode reaches the browser bundle, never the URL.

```bash
npm run build && npm run start                                            # sample
CORE_API_URL=http://localhost:8080 npm run build && CORE_API_URL=http://localhost:8080 npm run start   # live
```

## Sessions (live mode)

- `POST /v1/auth/login` with `tokenDelivery: cookie`. The access token (10 minutes) lives only
  in memory (`src/lib/api/session.ts`); never in localStorage, sessionStorage or a readable cookie.
- The refresh token is the core's `tramo_refresh` cookie (HttpOnly, Secure, SameSite=Strict,
  Path=/v1/auth). It works because the browser only ever talks to the web's origin.
- A reload asks `/v1/auth/refresh` for a new access token. A 401 refreshes once (one flight at a
  time, since the refresh token rotates) and retries; if that fails, back to `/entrar`.
- The tenant comes from the token. Several memberships: pick one on `/entrar`; switching company
  logs in again with `tenantId` (the core has no switch endpoint).
- Every decision POST carries an `Idempotency-Key` created per user action and reused on retry.

## Layout

`src/lib/api/`: `types.ts` (hand-written mirror of `eng/core/api/openapi.yaml`), `index.ts` (the
only door the screens use), `live.ts`, `sample.ts`, `fixtures.ts`. Screens are client components
in `src/components/app/views/`, because the access token is only in the browser.
