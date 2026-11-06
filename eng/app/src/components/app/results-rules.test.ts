import { test } from "node:test";
import assert from "node:assert/strict";
import { app } from "../../content/app";
import { metricTag } from "./results-rules";

test("in sample mode every measured number is tagged as an illustrative example", () => {
  assert.equal(metricTag("sample", { status: "measured", value: 82.5 }), app.results.sampleTag);
  assert.equal(metricTag("sample", { status: "measured", value: 0 }), app.results.sampleTag, "zero is still a number");
  assert.equal(app.results.sampleTag, "Ejemplo ilustrativo");
});

test("no data yet is not a number and carries no tag", () => {
  assert.equal(metricTag("sample", { status: "no_data", value: null }), null);
});

test("numbers from the core carry no sample tag", () => {
  assert.equal(metricTag("live", { status: "measured", value: 82.5 }), null);
  assert.equal(metricTag("live", { status: "no_data", value: null }), null);
});
