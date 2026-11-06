# web

Landing (`/`), client portal (`/app`), operator console (`/operador`), and the pages before a
session: login (`/entrar`), emailed link (`/ingresar`) and invitation (`/invitacion`).
Next.js 16 and Tailwind 4. Copy lives in `src/content/` (`site.ts` and `app.ts`, owned by producto).

## Portal on or off

`PORTAL_ENABLED` decides whether `/entrar`, `/ingresar`, `/invitacion`, `/app` and `/operador`
exist. Their route files are named `*.portal.tsx` and only count as routes when it is on
(`pageExtensions` in `next.config.ts`), so with it off they are not compiled at all.

| Command | Default | Override |
|---|---|---|
| `npm run build` (production) | off: landing only, the portal paths answer 404 | `PORTAL_ENABLED=1` |
| `npm run dev` | on | `PORTAL_ENABLED=0` |

Checked on 06/11: off builds only `/` and `/_not-found`; `/entrar`, `/app`, `/operador` and
`/v1/*` answer 404; the built output has no login copy, sample logins, `/v1/auth`, `/entrar`
or `/operador`. The landing never links to the portal.

## Where the data comes from (portal on)

| Setting | Mode | What the portal and the console read |
|---|---|---|
| neither | sample | Synthetic fixtures in the spec's shapes (`src/lib/api/fixtures.ts`), "Datos de ejemplo" banner. Sample logins are listed on `/entrar`. |
| `NEXT_PUBLIC_API_BASE=https://api.X` | live, production (D-18) | The core's API origin, called directly from the browser with CORS and `credentials: 'include'`. |
| `CORE_API_URL=http://localhost:8080` | live, local development | The core, through a same-origin rewrite of `/v1/*` on the web. |

Rules, enforced by `next.config.ts` (the build fails otherwise):

- `NEXT_PUBLIC_API_BASE` is an origin with no path, `https://` only; plain `http://` is accepted
  for a loopback host (`localhost`, `127.0.0.1`), to try a build on one machine.
- `CORE_API_URL` is the development proxy: `next dev` takes any origin, a build takes it only
  for a loopback host, never as the way production reaches the core.
- Not both at once.
- The web and the API share the registrable domain (`app.X` and `api.X`, D-18): the refresh
  cookie is SameSite=Strict and only travels within the same site.
- The core must list the web's origin in `CORS_ALLOWED_ORIGINS` (in dev, `APP_BASE_URL`), or
  refresh, switch-tenant and logout answer 403 `origin_not_allowed`.

Only the mode and the public API origin reach the browser bundle; `CORE_API_URL` never does,
and there is no API secret in the page. Both settings are compiled into the build: set them for
`npm run build` and keep them for `npm run start`.

```bash
npm run build && npm run start                                            # landing only (production default)
PORTAL_ENABLED=1 npm run build && PORTAL_ENABLED=1 npm run start          # portal on sample data
export PORTAL_ENABLED=1 NEXT_PUBLIC_API_BASE=https://api.tramo.example
npm run build && npm run start                                            # portal on the core, production
export PORTAL_ENABLED=1 CORE_API_URL=http://localhost:8080
npm run dev                                                               # portal on make dev, local
```

With `make dev`, set `APP_BASE_URL` and `DEV_APP_BASE_URL` to the web's exact origin (for
example `http://127.0.0.1:3000`): it is the allowed CORS origin and the base of the emailed links.

Sample mode keeps its session and decisions in memory. Moving through the app's own links keeps
them; a full page reload (or typing a URL) drops them and comes back as the area's default
sample person in their first company. Fine for demos and testing, not something to publish.

## Sessions (live mode)

- `POST /v1/auth/login` with `tokenDelivery: cookie`. The access token (10 minutes) lives only
  in memory (`src/lib/api/session.ts`); never in localStorage, sessionStorage or a readable cookie.
- The refresh token is the core's `tramo_refresh` cookie (HttpOnly, Secure, SameSite=Strict,
  Path=/v1/auth). Every call goes with `credentials: 'include'`, so it travels on refresh,
  switch-tenant and logout, whether the API is the web's own origin (dev) or `api.X` (production).
- A reload asks `/v1/auth/refresh` for a new access token. A 401 refreshes once and retries; if
  that fails, back to `/entrar`. Refresh and switch-tenant share one slot (one flight at a time),
  since both rotate the refresh token and a second use of an old one revokes the family.
- The tenant comes from the token. Several memberships: pick one on `/entrar`; choosing and
  "Cambiar de empresa" call `POST /v1/auth/switch-tenant`, no password. Opened directly or
  reloaded, the switch page restores the session from the cookie first.
- `/entrar` always offers "Entrar con un enlace por mail" (`POST /v1/auth/magic-link`), also
  after a 401 or a 429: an invited person with no password has no other way in, and the core
  always answers 202, so it reveals nothing about which emails exist (F32).
- `/ingresar` and `/invitacion` read the token from the URL fragment (never sent to a server),
  wipe it from the address bar and history on read, and send no referrer.
- The header shows the person's name from `/v1/me`, or the email while it is null; "Tu nombre"
  sets it with `PATCH /v1/me`.
- Every decision POST carries an `Idempotency-Key` created per user action and reused on retry.

## Client IP (live mode)

In production (D-18) the browser calls the API directly, so the client's address reaches the
core through our own Caddy in front of the API, as for any other client. The web is not in the
path and adds or forwards nothing.

The local `/v1` rewrite is different: it passes `X-Forwarded-For` through exactly as it arrives
and adds nothing (measured on Next 16.4.1 against a header echo). With `make dev`, leave the web
out of `TRUSTED_PROXIES`: the core then sees every user with the web's address, so login
throttling by IP counts all of them together. Fine for testing, not for a deployment.

## Layout

`src/lib/api/`: `types.ts` (hand-written mirror of `eng/core/api/openapi.yaml`), `index.ts` (the
only door the screens use), `live.ts`, `sample.ts`, `fixtures.ts`. Screens are client components
in `src/components/app/views/`, because the access token is only in the browser. The pages before
a session share `src/components/app/auth-frame.tsx`; the emailed tokens are handled in
`src/components/app/token-entry.tsx`.

## Tests

`npm test` runs `src/**/*.test.ts` with Node's own test runner (Node 22.18 or later strips the
types; `scripts/test-resolve.mjs` resolves the extensionless imports). No test dependency.
`src/lib/api/live.test.ts` checks that refresh and switch-tenant never overlap and that a 401
during a switch waits for it, against a fake core that revokes the family on a reused token.
`src/components/app/login-link.test.ts` checks F32 on /entrar: the emailed link is offered before
any login attempt and after any failed one, and with no email typed nothing is sent. The rules
live in `login-link.ts`, apart from the component, because the runner does not render React.
`src/components/app/invite-rules.test.ts` checks /invitacion: the form only opens with nobody
signed in on the browser (F33), and the password message is the one that applies to the fields
as they are now, not the one from the last attempt (F34).
`src/lib/api/session.test.ts` checks F33 across tabs with two copies of the session module: when
one tab signs in as someone else or out, the other drops its session at once.

Tabs (F33): every tab of a browser shares the refresh cookie, so in live mode each tab announces
the email it is signed in with on a BroadcastChannel (only the email, never a token). A tab that
hears another person, or nobody, drops its session and goes to `/entrar?motivo=otra-pestana`,
instead of showing the previous name and role until a reload. /invitacion first asks who is in
(a refresh) and, if someone is, says so and offers to sign out or cancel before showing the form.
It asks again right before sending: if someone signed in on another tab while the form was open,
it does not accept and goes back to saying who is in.
