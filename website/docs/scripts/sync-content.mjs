import fs from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { docs } from "../pages.mjs";
import { rewriteMarkdown } from "./links.mjs";

const repoRoot = path.resolve(import.meta.dirname, "../../..");
const outDir = path.resolve(import.meta.dirname, "../src");

const pages = [
  { src: "README.zh-CN.md", dest: "index.md" },
  { src: "README.md", dest: "en/index.md" },
  ...docs.flatMap((doc) => [
    { src: `docs/${doc.name}.zh-CN.md`, dest: `${doc.name}.md` },
    { src: `docs/${doc.name}.md`, dest: `en/${doc.name}.md` },
  ]),
];

export function syncContent({ clean = false } = {}) {
  if (clean) fs.rmSync(outDir, { recursive: true, force: true });
  fs.mkdirSync(outDir, { recursive: true });

  let written = 0;
  for (const page of pages) {
    const source = path.join(repoRoot, page.src);
    if (!fs.existsSync(source)) {
      throw new Error(`missing docs source: ${page.src}`);
    }
    const body = rewriteMarkdown(fs.readFileSync(source, "utf8"));
    if (
      /\]\([^)]*\.zh-CN\.md/.test(body) ||
      /\]\((?:\.\.\/)*docs\//.test(body) ||
      /\]\(\.\.\//.test(body) ||
      /<a id=/.test(body)
    ) {
      throw new Error(`unrewritten link or anchor left in ${page.src}`);
    }
    const dest = path.join(outDir, page.dest);
    fs.mkdirSync(path.dirname(dest), { recursive: true });
    const previous = fs.existsSync(dest) ? fs.readFileSync(dest, "utf8") : null;
    if (previous !== body) {
      fs.writeFileSync(dest, body);
      written += 1;
    }
  }

  return { pages: pages.length, written };
}

const invokedDirectly = process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href;
if (invokedDirectly) {
  const { pages: count } = syncContent({ clean: true });
  console.log(`synced ${count} markdown pages into ${path.relative(repoRoot, outDir)}`);
}
