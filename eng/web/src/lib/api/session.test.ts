// F33: tabs of one browser share the refresh cookie, so a tab whose person changed or left in
// another tab drops its session instead of showing the previous name and role.
import { test } from "node:test";
import assert from "node:assert/strict";
import { peerEnds } from "./session";

test("a tab drops its session when another announces someone else or nobody", () => {
  const owner = { email: "owner@alfa.test" };
  assert.equal(peerEnds(owner, "inv3@alfa-sint.test"), true);
  assert.equal(peerEnds(owner, null), true);
  assert.equal(peerEnds(owner, "owner@alfa.test"), false);
  assert.equal(peerEnds(null, "inv3@alfa-sint.test"), false);
  assert.equal(peerEnds(null, null), false);
});

// Two tabs for real: two copies of the session module, each with its own memory and its own
// BroadcastChannel, in live mode. The channels are unref'd so the test process can end.
type Mod = typeof import("./session");
const person = (email: string) => ({ accessToken: "t", tenantId: "a", tenantName: "Alfa", role: "owner" as const, email, name: null, memberships: [] });
const settle = () => new Promise((r) => setTimeout(r, 50));

test("accepting an invitation in one tab ends the other tab's session, live", async () => {
  process.env.TRAMO_CORE_MODE = "live";
  const g = globalThis as Record<string, unknown>;
  g.window ??= globalThis;
  const Base = globalThis.BroadcastChannel;
  g.BroadcastChannel = class extends Base {
    constructor(name: string) {
      super(name);
      (this as unknown as { unref(): void }).unref();
    }
  };
  const tab = (id: string): Promise<Mod> => import(new URL(`./session.ts?tab=${id}`, import.meta.url).href);
  const [owner, invite] = await Promise.all([tab("owner"), tab("invite")]);
  assert.equal(owner.getMode(), "live");

  owner.setSession(person("owner@alfa.test"));
  await settle();
  // The invite tab asks who is in: the same person, so the owner's tab stays as it is.
  invite.setSession(person("owner@alfa.test"));
  await settle();
  assert.equal(owner.getSession()?.email, "owner@alfa.test");
  assert.equal(owner.endedElsewhere(), false);

  // The invited person gets in: the owner's tab learns and lets go.
  let heard = 0;
  owner.subscribe(() => heard++);
  invite.setSession(person("inv3@alfa-sint.test"));
  await settle();
  assert.equal(owner.getSession(), null);
  assert.equal(owner.endedElsewhere(), true);
  assert.equal(heard, 1);

  // A logout in one tab ends the others too.
  owner.setSession(person("inv3@alfa-sint.test"));
  await settle();
  assert.equal(owner.endedElsewhere(), false);
  invite.setSession(null);
  await settle();
  assert.equal(owner.getSession(), null);
  assert.equal(owner.endedElsewhere(), true);
});
