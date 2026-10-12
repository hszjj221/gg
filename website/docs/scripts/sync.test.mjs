import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";
import { syncContent } from "./sync-content.mjs";

const generated = path.resolve(import.meta.dirname, "../src/en/index.md");

test("sync rewrites a stale generated page and leaves an unchanged tree alone", () => {
  syncContent();
  const original = fs.readFileSync(generated, "utf8");
  fs.writeFileSync(generated, `${original}\nstale\n`);
  const repaired = syncContent();
  assert.equal(repaired.written, 1);
  assert.equal(fs.readFileSync(generated, "utf8"), original);
  const again = syncContent();
  assert.equal(again.written, 0);
});
