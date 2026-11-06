// Sample mode's name rule must answer like the core's (internal/identity/name.go), including the
// edges where JavaScript and Go disagree on what to trim.
import { test } from "node:test";
import assert from "node:assert/strict";
import { addMember, listMembers, login, removeMember, updateName } from "./sample";
import { ApiError } from "./errors";

const cases: [string, string | null][] = [
  ["Ana", "Ana"],
  ["  Ana Pérez \n", "Ana Pérez"], // Go's TrimSpace also trims line breaks at the edges
  ["﻿Ana", null], // a BOM: trim() would drop it, Go keeps it and refuses it (Cf)
  ["Ana﻿", null],
  ["A​na", null], // zero width space (Cf)
  ["Ana‮Z", null], // bidi override (Cf)
  ["Ana\nPérez", null], // a line break inside (Cc)
  ["ㅤ", null], // Hangul filler, looks blank
  ["⠀", null], // Braille blank
  ["...", null], // no letter or number
  ["★", null], // a symbol alone
  ["x".repeat(120), "x".repeat(120)],
  ["x".repeat(121), null],
  ["   ", null],
];

test("updateName follows the core's name rules", async () => {
  await login("titular@agro.example", "ejemplo");
  for (const [input, want] of cases) {
    const label = JSON.stringify(input).slice(0, 24);
    if (want === null) {
      await assert.rejects(updateName(input), (e: unknown) => e instanceof ApiError && e.code === "invalid_name", `${label} should be refused`);
    } else {
      assert.equal(await updateName(input), want, `${label} should be stored as ${JSON.stringify(want).slice(0, 24)}`);
    }
  }
});

// Members answer like the core (D-19): adding is a pending invitation; removing the only owner is 409.
const code = (c: string) => (e: unknown) => e instanceof ApiError && e.code === c;

test("members: an owner invites (pending), re-invites, and can not invite someone already in", async () => {
  await login("titular@agro.example", "ejemplo");
  const m = await addMember(" Nueva@Agro.Example ", "approver");
  assert.deepEqual({ email: m.email, role: m.role, status: m.status, name: m.name }, { email: "nueva@agro.example", role: "approver", status: "pending", name: null });
  await addMember("nueva@agro.example", "viewer");
  const pending = (await listMembers()).items.filter((x) => x.status === "pending");
  assert.deepEqual(pending.map((x) => [x.email, x.role]), [["nueva@agro.example", "viewer"]]);
  await assert.rejects(addMember("aprobador@agro.example", "viewer"), code("already_member"));
  await assert.rejects(addMember("no es un mail", "viewer"), code("invalid_email"));
  await assert.rejects(addMember("x@agro.example", "operator"), code("forbidden"));
});

test("members: removing a pending invitation, a person, and never the only owner", async () => {
  await login("titular@agro.example", "ejemplo");
  const p = await addMember("quitar@agro.example", "viewer");
  await removeMember(p.userId);
  assert.ok(!(await listMembers()).items.some((x) => x.email === "quitar@agro.example"));
  const me = (await listMembers()).items.find((x) => x.email === "titular@agro.example")!;
  await assert.rejects(removeMember(me.userId), code("last_owner"));
  const viewer = (await listMembers()).items.find((x) => x.email === "lectura@agro.example")!;
  await removeMember(viewer.userId);
  await assert.rejects(removeMember(viewer.userId), code("not_found"));
});

test("members: only an owner removes; an approver gets 403", async () => {
  await login("aprobador@agro.example", "ejemplo");
  const someone = (await listMembers()).items[0];
  await assert.rejects(removeMember(someone.userId), code("forbidden"));
  await assert.rejects(addMember("x@agro.example", "viewer"), code("forbidden"));
});
