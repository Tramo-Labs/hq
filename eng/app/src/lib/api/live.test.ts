// The shared slot between refresh and switch-tenant (live.ts). Both rotate the refresh token,
// and the core revokes the whole family when a token is used twice, so they must never overlap.
// A fake core in memory plays the browser's cookie jar and the core's rotation rule.
import { test, beforeEach } from "node:test";
import assert from "node:assert/strict";
import { acceptInviteAs, previewInvite, refresh, request, switchTenant } from "./live";
import { getSession, setSession } from "./session";

const T1 = "00000000-0000-4000-8000-000000000001";
const T2 = "00000000-0000-4000-8000-000000000002";

type Core = {
  jar: string; // the refresh cookie as the browser holds it
  current: string; // the refresh token the core accepts now
  revoked: boolean; // the family was revoked (a token was used twice)
  issued: number;
  validAccess: Set<string>;
  inFlight: number;
  maxInFlight: number;
  auth: string[]; // rotating calls, in the order they reached the core
  delayMs: number;
};
let core: Core;

const reply = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": status < 300 ? "application/json" : "application/problem+json" } });
const tick = (ms: number) => new Promise((r) => setTimeout(r, ms));

async function fakeFetch(input: string | URL | Request, init: RequestInit = {}): Promise<Response> {
  const path = new URL(String(input), "http://web.test").pathname;
  const headers = new Headers(init.headers);

  if (path === "/v1/auth/refresh" || path === "/v1/auth/switch-tenant") {
    const presented = core.jar; // the browser attaches the cookie it has when the request leaves
    core.auth.push(path.slice("/v1/auth/".length));
    core.inFlight++;
    core.maxInFlight = Math.max(core.maxInFlight, core.inFlight);
    try {
      await tick(core.delayMs);
      if (core.revoked) return reply(401, { code: "unauthorized" });
      if (presented !== core.current) {
        core.revoked = true;
        return reply(401, { code: "refresh_token_reused" });
      }
      core.issued++;
      core.current = `r${core.issued}`;
      core.jar = core.current; // Set-Cookie on the answer
      const access = `a${core.issued}`;
      core.validAccess.add(access);
      const body = JSON.parse(String(init.body ?? "{}")) as { tenantId?: string };
      return reply(200, { accessToken: access, tokenType: "Bearer", expiresIn: 600, tenantId: body.tenantId ?? getSession()?.tenantId ?? T1, role: "operator" });
    } finally {
      core.inFlight--;
    }
  }

  const bearer = headers.get("Authorization")?.replace("Bearer ", "") ?? "";
  if (!core.validAccess.has(bearer)) return reply(401, { code: "unauthorized" });
  if (path === "/v1/me") {
    return reply(200, {
      userId: "u", email: "operador@tramo.example", name: null, activeTenantId: T1, role: "operator",
      memberships: [{ tenantId: T1, tenantName: "Uno", role: "operator" }, { tenantId: T2, tenantName: "Dos", role: "operator" }],
    });
  }
  return reply(200, { items: [], seenWith: bearer });
}

beforeEach(() => {
  core = { jar: "r0", current: "r0", revoked: false, issued: 0, validAccess: new Set(), inFlight: 0, maxInFlight: 0, auth: [], delayMs: 30 };
  globalThis.fetch = fakeFetch as typeof fetch;
  // A session whose access token the core no longer takes, as after its 10 minutes.
  setSession({ accessToken: "a-expired", tenantId: T1, tenantName: "Uno", role: "operator", email: "operador@tramo.example", name: null, memberships: [] });
});

test("a refresh in flight when the switch starts: one at a time, the family survives", async () => {
  const refreshing = refresh();
  const switching = switchTenant(T2);
  await Promise.all([refreshing, switching]);

  assert.equal(core.maxInFlight, 1, "refresh and switch-tenant overlapped");
  assert.deepEqual(core.auth, ["refresh", "switch-tenant"]);
  assert.equal(core.revoked, false, "the refresh token family was revoked");
  assert.equal(getSession()?.tenantId, T2);
  assert.equal(getSession()?.accessToken, "a2");
});

test("a 401 while the switch is in flight waits for it instead of refreshing on its own", async () => {
  const switching = switchTenant(T2);
  const read = request("/v1/ledger/accounts"); // its expired token gets a 401 mid switch
  const [, res] = await Promise.all([switching, read]);

  assert.equal(core.maxInFlight, 1);
  assert.deepEqual(core.auth, ["switch-tenant"], "the 401 started its own refresh");
  assert.equal(core.revoked, false);
  assert.equal(res.status, 200);
  assert.equal(((await res.json()) as { seenWith: string }).seenWith, "a1", "the retry did not use the switch's new token");
  assert.equal(getSession()?.tenantId, T2);
});

test("two refreshes at once share one flight", async () => {
  const [a, b] = await Promise.all([refresh(), refresh()]);
  assert.equal(a, b);
  assert.deepEqual(core.auth, ["refresh"]);
  assert.equal(core.revoked, false);
});

// F38: the invitation preview is anonymous and keeps the token out of the URL.
test("previewInvite posts the token in the body only, with no session and no Idempotency-Key", async () => {
  const token = "t".repeat(43);
  const seen: { url: string; method?: string; headers: Headers; body: unknown }[] = [];
  globalThis.fetch = (async (input: string | URL | Request, init: RequestInit = {}) => {
    seen.push({ url: String(input), method: init.method, headers: new Headers(init.headers), body: JSON.parse(String(init.body)) });
    return reply(200, { tenantName: "Molino Alfa", role: "viewer" });
  }) as typeof fetch;
  setSession({ accessToken: "a-secret", tenantId: T1, tenantName: "x", role: "owner", email: "o@x.test", name: null, memberships: [] });
  assert.deepEqual(await previewInvite(token), { tenantName: "Molino Alfa", role: "viewer" });
  assert.equal(seen.length, 1);
  const [r] = seen;
  assert.equal(new URL(r.url, "http://web.test").pathname, "/v1/auth/invite/preview");
  assert.equal(r.method, "POST");
  assert.ok(!r.url.includes(token), "the token never goes in the URL");
  assert.deepEqual(r.body, { token });
  assert.equal(r.headers.get("Authorization"), null);
  assert.equal(r.headers.get("Idempotency-Key"), null);
});

// F41: accepting for the account that is in. A dead link (401 invalid_invite) must never end the
// session; only an access token the core refuses (401 unauthorized) is refreshed, once.
function acceptCore(first: Response) {
  const seen: { path: string; auth: string | null; body: unknown }[] = [];
  globalThis.fetch = (async (input: string | URL | Request, init: RequestInit = {}) => {
    const path = new URL(String(input), "http://web.test").pathname;
    const headers = new Headers(init.headers);
    seen.push({ path, auth: headers.get("Authorization"), body: init.body ? JSON.parse(String(init.body)) : null });
    if (path === "/v1/auth/refresh") return reply(200, { accessToken: "fresh", tenantId: T1, role: "owner" });
    if (path === "/v1/me") return reply(200, { userId: "u", email: "o@x.test", name: null, activeTenantId: T1, role: "owner", memberships: [{ tenantId: T1, tenantName: "x", role: "owner" }] });
    const accepts = seen.filter((s) => s.path === "/v1/auth/invite/accept").length;
    return accepts === 1 ? first.clone() : new Response(null, { status: 204 });
  }) as typeof fetch;
  setSession({ accessToken: "stale", tenantId: T1, tenantName: "x", role: "owner", email: "o@x.test", name: null, memberships: [] });
  return seen;
}
const invite = "i".repeat(43);

test("acceptInviteAs sends the session's token and only the invitation token; 204 leaves the session as is", async () => {
  const seen = acceptCore(new Response(null, { status: 204 }));
  await acceptInviteAs(invite);
  assert.deepEqual(seen, [{ path: "/v1/auth/invite/accept", auth: "Bearer stale", body: { token: invite } }]);
  assert.equal(getSession()?.accessToken, "stale");
});

test("a dead invitation link with a session open does not refresh and does not end the session", async () => {
  for (const code of ["invalid_invite", "invite_for_another_account"]) {
    const seen = acceptCore(reply(code === "invalid_invite" ? 401 : 409, { code }));
    await assert.rejects(acceptInviteAs(invite), (e: unknown) => (e as { code?: string }).code === code);
    assert.deepEqual(seen.map((s) => s.path), ["/v1/auth/invite/accept"], code);
    assert.equal(getSession()?.email, "o@x.test", code);
  }
});

test("an access token the core refuses is refreshed once and the accept retried with the new one", async () => {
  const seen = acceptCore(reply(401, { code: "unauthorized" }));
  await acceptInviteAs(invite);
  const accepts = seen.filter((s) => s.path === "/v1/auth/invite/accept").map((s) => s.auth);
  assert.deepEqual(accepts, ["Bearer stale", "Bearer fresh"]);
});

test("previewInvite answers like accept: the core's error comes back as is", async () => {
  globalThis.fetch = (async () => reply(403, { code: "not_a_member" })) as typeof fetch;
  await assert.rejects(previewInvite("t".repeat(43)), (e: unknown) => (e as { code?: string }).code === "not_a_member");
});
