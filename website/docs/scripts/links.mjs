const GITHUB_BLOB = "https://github.com/hszjj221/gg/blob/main/";

// Rewrite relative links so pages generated from the repo still resolve.
// Chinese docs are the site root. English docs live under /en/.
export function rewriteHref(raw) {
  const original = String(raw).trim();
  if (original === "" || original.startsWith("#") || original.startsWith("//")) return original;
  if (/^[a-z][a-z0-9+.-]*:/i.test(original)) return original;

  const hashAt = original.indexOf("#");
  const hash = hashAt >= 0 ? original.slice(hashAt) : "";
  let href = (hashAt >= 0 ? original.slice(0, hashAt) : original).replace(/^\.\//, "");
  if (href === "") return `${hash}`;

  if (href === "SECURITY.md" || href === "LICENSE") return GITHUB_BLOB + href;
  if (href === "README.zh-CN.md") return `/${hash}`;
  if (href === "README.md") return `/en/${hash}`;

  let match = href.match(/^(?:docs\/)?([A-Za-z0-9-]+)\.zh-CN\.md$/);
  if (match) return `/${match[1]}${hash}`;
  match = href.match(/^(?:docs\/)?([A-Za-z0-9-]+)\.md$/);
  if (match) return `/en/${match[1]}${hash}`;
  return original;
}

export function rewriteMarkdown(text) {
  const lines = text.split("\n");
  let fence = null;
  return lines
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
}
