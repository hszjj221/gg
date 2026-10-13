import { spawn } from "node:child_process";
import path from "node:path";
import { syncContent } from "./sync-content.mjs";
import { watchSources } from "./watch-sources.mjs";

const docsDir = path.resolve(import.meta.dirname, "..");
const repoRoot = path.resolve(docsDir, "../..");

syncContent({ clean: true });

const vitepress = path.join(docsDir, "node_modules", ".bin", "vitepress");
const child = spawn(vitepress, ["dev", ...process.argv.slice(2)], {
  cwd: docsDir,
  stdio: "inherit",
});

let timer = null;
function resync() {
  clearTimeout(timer);
  timer = setTimeout(() => {
    try {
      const { written } = syncContent();
      if (written > 0) console.log(`resynced ${written} docs page(s)`);
    } catch (error) {
      console.error(error instanceof Error ? error.message : error);
    }
  }, 40);
}

const stop = watchSources(resync, {
  root: repoRoot,
  docsDir: path.join(repoRoot, "docs"),
});

child.on("error", (error) => {
  clearTimeout(timer);
  stop();
  console.error(error.message);
  process.exit(1);
});

child.on("exit", (code, signal) => {
  clearTimeout(timer);
  stop();
  if (signal) {
    process.kill(process.pid, signal);
    return;
  }
  process.exit(code ?? 0);
});
