import fs from "node:fs";
import path from "node:path";

export function shouldResync(kind, filename) {
  const base = path.basename(String(filename ?? ""));
  if (base === "") return false;
  if (kind === "root") return base === "README.md" || base === "README.zh-CN.md";
  if (kind === "docs") return base.endsWith(".md");
  return false;
}

// Watch the repository README files and docs/ markdown. VitePress only sees the
// generated copies under website/docs/src, so this is what keeps `npm run dev` current.
export function watchSources(onChange, dirs) {
  const notify = (kind) => (_event, filename) => {
    if (shouldResync(kind, filename)) onChange();
  };
  const watchers = [
    fs.watch(dirs.root, notify("root")),
    fs.watch(dirs.docsDir, notify("docs")),
  ];
  return () => {
    for (const watcher of watchers) watcher.close();
  };
}
