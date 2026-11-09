// Sample mode's Mercado Pago must answer like the core's spec: one state per company at the
// start, the state rules on connect, and the disconnect that asks to remove the app in MP too.
import { test } from "node:test";
import assert from "node:assert/strict";
import { authorizeMercadoPago, connectMercadoPago, disconnectMercadoPago, getMercadoPago, login } from "./sample";
import { ApiError } from "./errors";

const refused = (code: string) => (e: unknown) => e instanceof ApiError && e.code === code;
const stateOf = (url: string) => new URL(url, "http://web.test").searchParams.get("state")!;

test("each sample company starts in its own state", async () => {
  await login("titular@agro.example", "ejemplo");
  assert.equal((await getMercadoPago()).status, "disconnected");
  await login("titular@frio.example", "ejemplo");
  const frio = await getMercadoPago();
  assert.equal(frio.status, "connected");
  assert.ok(frio.lastImportAt);
  await login("titular@ferr.example", "ejemplo");
  assert.equal((await getMercadoPago()).status, "needs_reconnect");
  await login("titular@trans.example", "ejemplo");
  assert.equal((await getMercadoPago()).configured, false);
});

test("approver and viewer get 403, like the core", async () => {
  for (const who of ["aprobador@agro.example", "lectura@agro.example"]) {
    await login(who, "ejemplo");
    await assert.rejects(getMercadoPago(), refused("forbidden"), who);
  }
});

test("connect: once per state, only by who started it", async () => {
  await login("titular@agro.example", "ejemplo");
  const { authorizeUrl } = await authorizeMercadoPago();
  assert.ok(authorizeUrl.startsWith("/app/mercadopago/vuelta?"), "sample stays in the portal");
  const state = stateOf(authorizeUrl);

  await assert.rejects(connectMercadoPago("ejemplo", "not-a-state"), refused("state_invalid"));
  const conn = await connectMercadoPago("ejemplo", state);
  assert.equal(conn.status, "connected");
  assert.equal(conn.lastImportAt, undefined, "connected, nothing imported yet");
  await assert.rejects(connectMercadoPago("ejemplo", state), refused("state_used"));

  const other = stateOf((await authorizeMercadoPago()).authorizeUrl);
  await login("titular@frio.example", "ejemplo");
  await assert.rejects(connectMercadoPago("ejemplo", other), refused("state_not_yours"));
});

test("a code Mercado Pago would not take is mp_code_rejected", async () => {
  await login("titular@agro.example", "ejemplo");
  const state = stateOf((await authorizeMercadoPago()).authorizeUrl);
  await assert.rejects(connectMercadoPago("otro", state), refused("mp_code_rejected"));
});

test("without credentials, authorize is 501 not_configured", async () => {
  await login("titular@trans.example", "ejemplo");
  await assert.rejects(authorizeMercadoPago(), refused("not_configured"));
});

test("only the owner starts or ends a connection", async () => {
  await login("aprobador@agro.example", "ejemplo");
  await assert.rejects(authorizeMercadoPago(), refused("forbidden"));
  await assert.rejects(disconnectMercadoPago(), refused("forbidden"));
});

test("disconnecting a connection asks to remove the app in Mercado Pago too", async () => {
  await login("titular@frio.example", "ejemplo");
  const after = await disconnectMercadoPago();
  assert.equal(after.status, "disconnected");
  assert.equal(after.revokeInMercadoPago, true);
  assert.equal((await getMercadoPago()).status, "disconnected");
});
