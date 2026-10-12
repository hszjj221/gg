import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { shouldResync, watchSources } from "./watch-sources.mjs";

test("only the README files and docs markdown schedule a resync", () => {
  assert.equal(shouldResync("root", "README.md"), true);
  assert.equal(shouldResync("root", "README.zh-CN.md"), true);
  assert.equal(shouldResync("root", "CONTRIBUTING.md"), false);
  assert.equal(shouldResync("root", null), false);
  assert.equal(shouldResync("docs", "usage.md"), true);
  assert.equal(shouldResync("docs", "usage.zh-CN.md"), true);
  assert.equal(shouldResync("docs", "demo.tape"), false);
});

function delay(ms) {
  return new Promise((resolve) => {
    setTimeout(resolve, ms);
  });
}

async function waitFor(predicate) {
  const start = Date.now();
  while (!predicate()) {
    if (Date.now() - start > 2000) throw new Error("timed out waiting for a docs resync");
    await delay(20);
  }
}

test("a README or docs edit notifies the watcher", async () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "gg-docs-watch-"));
  const docsDir = path.join(root, "docs");
  fs.mkdirSync(docsDir);
  const readme = path.join(root, "README.md");
  const usage = path.join(docsDir, "usage.md");
  fs.writeFileSync(readme, "# a\n");
  fs.writeFileSync(usage, "# u\n");
  let hits = 0;
  const stop = watchSources(() => {
    hits += 1;
  }, { root, docsDir });
  try {
    fs.writeFileSync(readme, "# b\n");
    await waitFor(() => hits > 0);
    const afterReadme = hits;
    fs.writeFileSync(path.join(root, "CONTRIBUTING.md"), "nope\n");
    await delay(200);
    assert.equal(hits, afterReadme);
    fs.writeFileSync(usage, "# c\n");
    await waitFor(() => hits > afterReadme);
  } finally {
    stop();
    fs.rmSync(root, { recursive: true, force: true });
  }
});
