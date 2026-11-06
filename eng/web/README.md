# web

Landing (`/`), client portal (`/app`), operator console (`/operador`) and login (`/entrar`).
Next.js 16 and Tailwind 4. Copy lives in `src/content/` (`site.ts` and `app.ts`, owned by producto).

## Portal on or off

`PORTAL_ENABLED` decides whether `/entrar`, `/app` and `/operador` exist. Their route files are
named `*.portal.tsx` and only count as routes when it is on (`pageExtensions` in
`next.config.ts`), so with it off they are not compiled at all.

| Command | Default | Override |
|---|---|---|
| `npm run build` (production) | off: landing only, the portal paths answer 404 | `PORTAL_ENABLED=1` |
| `npm run dev` | on | `PORTAL_ENABLED=0` |

Checked on 06/11: off builds only `/` and `/_not-found`; `/entrar`, `/app`, `/operador` and
`/v1/*` answer 404; the built output has no login copy, sample logins, `/v1/auth`, `/entrar`
or `/operador`. On builds all ten routes. The landing never links to the portal.

## Two modes (portal on)

| `CORE_API_URL` | Mode | What the portal and the console read |
|---|---|---|
| unset | sample | Synthetic fixtures in the spec's shapes (`src/lib/api/fixtures.ts`), "Datos de ejemplo" banner. Sample logins are listed on `/entrar`. |
| `http://host:port` | live | The core, through a same-origin rewrite of `/v1/*` (`next.config.ts`). No banner. |

The mode and the rewrite are compiled into the build: set `CORE_API_URL` for `npm run build`
and keep it for `npm run start`. Only the mode reaches the browser bundle, never the URL.

```bash
npm run build && npm run start                                            # landing only (production default)
PORTAL_ENABLED=1 npm run build && PORTAL_ENABLED=1 npm run start          # portal on sample data
export PORTAL_ENABLED=1 CORE_API_URL=http://localhost:8080
npm run build && npm run start                                            # portal on the core
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

## Client IP (live mode)

The `/v1` rewrite passes `X-Forwarded-For` through exactly as it arrives and adds nothing:
measured on Next 16.4.1 against a header echo, a request without the header reaches the core
without it, and one with `X-Forwarded-For: 203.0.113.9` reaches it with that value. The web
cannot see the browser's address itself, so the real IP has to come from the proxy in front.

- Production: Caddy in front of the web sets the header to the client's address, and the
  core lists the web's address in `TRUSTED_PROXIES`. That only holds if the web is reachable
  through Caddy alone; reachable directly, any client could write its own header.
- Local (`make dev` with the web on its own port): leave the web out of `TRUSTED_PROXIES`.
  The core then sees every user with the web's address, so login throttling by IP counts
  all of them together. Fine for testing, not for a deployment.

## Layout

`src/lib/api/`: `types.ts` (hand-written mirror of `eng/core/api/openapi.yaml`), `index.ts` (the
only door the screens use), `live.ts`, `sample.ts`, `fixtures.ts`. Screens are client components
in `src/components/app/views/`, because the access token is only in the browser.
