import assert from "node:assert/strict";
import test from "node:test";
import { rewriteHref, rewriteMarkdown } from "./links.mjs";

test("rewrites README and docs links onto the locale paths", () => {
  assert.equal(rewriteHref("README.zh-CN.md"), "/");
  assert.equal(rewriteHref("README.md"), "/en/");
  assert.equal(rewriteHref("docs/architecture.md"), "/en/architecture");
  assert.equal(rewriteHref("docs/architecture.md#security-model"), "/en/architecture#security-model");
  assert.equal(rewriteHref("docs/architecture.zh-CN.md"), "/architecture");
  assert.equal(rewriteHref("docs/architecture.zh-CN.md#安全模型"), "/architecture#安全模型");
  assert.equal(rewriteHref("diagnostics.md"), "/en/diagnostics");
  assert.equal(rewriteHref("diagnostics.zh-CN.md"), "/diagnostics");
  assert.equal(rewriteHref("./rag.md"), "/en/rag");
  assert.equal(
    rewriteHref("SECURITY.md"),
    "https://github.com/hszjj221/gg/blob/main/SECURITY.md",
  );
  assert.equal(rewriteHref("LICENSE"), "https://github.com/hszjj221/gg/blob/main/LICENSE");
  assert.equal(rewriteHref("https://t.me/BotFather"), "https://t.me/BotFather");
  assert.equal(rewriteHref("#security-model"), "#security-model");
});

test("does not rewrite links inside fenced code", () => {
  const input = ["See [arch](docs/architecture.md).", "", "```bash", "cat docs/architecture.md", "```", ""].join("\n");
  const out = rewriteMarkdown(input);
  assert.match(out, /\]\(\/en\/architecture\)/);
  assert.match(out, /cat docs\/architecture\.md/);
});

test("rewrites a prose link and leaves the badge URL", () => {
  const input = "[![CI](https://example.test/badge.svg)](https://example.test) and [guide](docs/rag.md).";
  const out = rewriteMarkdown(input);
  assert.match(out, /https:\/\/example\.test\/badge\.svg/);
  assert.match(out, /\]\(\/en\/rag\)/);
});
