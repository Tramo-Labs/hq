// The route the portal asks the core for the files that did not enter. The mode is read once
// when session.ts loads, so live is set before index.ts is imported, and fetch is recorded.
import { test } from "node:test";
import assert from "node:assert/strict";

test("live, the quarantine list is the files list with status=quarantined and no period", async () => {
  process.env.TRAMO_CORE_MODE = "live";
  const asked: string[] = [];
  globalThis.fetch = (async (input: string | URL | Request) => {
    asked.push(String(input));
    return new Response(JSON.stringify({ items: [], truncated: true }), { status: 200, headers: { "Content-Type": "application/json" } });
  }) as typeof fetch;

  const api = await import("./index");
  const session = await import("./session"); // the same module instance index.ts reads
  assert.equal(api.getMode(), "live");
  session.setSession({ accessToken: "t", tenantId: "a", tenantName: "Alfa", role: "owner", email: "o@alfa.test", name: null, memberships: [] });

  const list = await api.listQuarantine();
  assert.equal(asked.length, 1);
  const url = new URL(asked[0], "http://web.test");
  assert.equal(url.pathname, "/v1/ledger/files");
  assert.equal(url.searchParams.get("status"), "quarantined");
  assert.equal(url.searchParams.has("period"), false, "no month filter: the most recent files whatever the month");
  assert.equal(list.truncated, true, "truncated reaches the view");
});
