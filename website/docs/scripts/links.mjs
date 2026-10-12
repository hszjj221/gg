const GITHUB_BLOB = "https://github.com/hszjj221/gg/blob/main/";

// Rewrite relative links so pages generated from the repo still resolve.
// Chinese docs are the site root. English docs live under /en/.
export function rewriteHref(raw) {
  const original = String(raw).trim();
  if (original === "" || original.startsWith("#") || original.startsWith("//")) return original;
  if (/^[a-z][a-z0-9+.-]*:/i.test(original)) return original;

  const hashAt = original.indexOf("#");
  const hash = hashAt >= 0 ? original.slice(hashAt) : "";
  let href = hashAt >= 0 ? original.slice(0, hashAt) : original;
  while (href.startsWith("./") || href.startsWith("../")) {
    href = href.startsWith("./") ? href.slice(2) : href.slice(3);
  }
  if (href === "") return hash || original;

  if (href === "SECURITY.md" || href === "LICENSE" || href === "CONTRIBUTING.md" || href === "docs/demo.tape") {
    return GITHUB_BLOB + href;
  }
  if (href === "README.zh-CN.md") return `/${hash}`;
  if (href === "README.md") return `/en/${hash}`;

  let match = href.match(/^(?:docs\/)?([A-Za-z0-9-]+)\.zh-CN\.md$/);
  if (match) return `/${match[1]}${hash}`;
  match = href.match(/^(?:docs\/)?([A-Za-z0-9-]+)\.md$/);
  if (match) return `/en/${match[1]}${hash}`;
  return original;
}

// Explicit <a id> anchors are how the usage guide and the Chinese architecture
// page keep stable hashes. VitePress rejects other raw tags, so fold each
// anchor onto the heading it introduces.
export function hoistHtmlAnchors(text) {
  const lines = text.split("\n");
  let fence = null;
  const out = [];
  for (let i = 0; i < lines.length; i += 1) {
    const line = lines[i];
    const open = line.match(/^(\s*)(`{3,}|~{3,})/);
    if (open) {
      const marker = open[2][0];
      fence = !fence ? marker : fence === marker ? null : fence;
      out.push(line);
      continue;
    }
    if (!fence) {
      const anchor = line.match(/^<a id="([A-Za-z0-9_-]+)"><\/a>\s*$/);
      if (anchor) {
        let next = i + 1;
        while (next < lines.length && lines[next].trim() === "") next += 1;
        const heading = lines[next] && lines[next].match(/^(#{1,6} .+)$/);
        if (heading && !heading[1].includes("{#")) {
          out.push(`${heading[1]} {#${anchor[1]}}`);
          i = next;
          continue;
        }
      }
    }
    out.push(line);
  }
  return out.join("\n");
}

export function rewriteMarkdown(text) {
  const lines = text.split("\n");
  let fence = null;
  const rewritten = lines
    .map((line) => {
      const open = line.match(/^(\s*)(`{3,}|~{3,})/);
      if (open) {
        const marker = open[2][0];
        if (!fence) fence = marker;
        else if (fence === marker) fence = null;
        return line;
      }
      if (fence) return line;
      return line.replace(/(!?\[[^\]]*\])\(([^)\s]+)(\s+"[^"]*")?\)/g, (full, label, href, title = "") => {
        const next = rewriteHref(href);
        return `${label}(${next}${title || ""})`;
      });
    })
    .join("\n");
  return hoistHtmlAnchors(rewritten);
}
