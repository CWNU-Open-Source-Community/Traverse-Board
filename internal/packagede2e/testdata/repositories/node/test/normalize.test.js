"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { normalizeTitle } = require("../src/normalize.js");

test("normalizes repeated whitespace without losing Unicode", () => {
  assert.equal(normalizeTitle("  Traverse   针路簿  "), "traverse 针路簿");
});
