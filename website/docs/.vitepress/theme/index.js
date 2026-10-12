import DefaultTheme from "vitepress/theme";
import "./custom.css";

function fitMermaid() {
  for (const svg of document.querySelectorAll(".mermaid svg")) {
    const box = svg.viewBox && svg.viewBox.baseVal;
    if (!box || !box.width) continue;
    svg.style.width = `${box.width}px`;
    svg.style.maxWidth = "none";
    svg.style.height = `${box.height}px`;
  }
}

function revealHash() {
  const raw = window.location.hash.slice(1);
  if (!raw) return;
  let id = raw;
  try {
    id = decodeURIComponent(raw);
  } catch {
    return;
  }
  const target = document.getElementById(id);
  if (target) target.scrollIntoView();
}

export default {
  extends: DefaultTheme,
  enhanceApp({ router }) {
    if (typeof window === "undefined") return;
    const run = () => {
      fitMermaid();
      revealHash();
      window.setTimeout(() => {
        fitMermaid();
        revealHash();
      }, 80);
      window.setTimeout(() => {
        fitMermaid();
        revealHash();
      }, 500);
    };
    const previous = router.onAfterRouteChanged;
    router.onAfterRouteChanged = (to) => {
      if (typeof previous === "function") previous(to);
      run();
    };
    window.addEventListener("load", run);
  },
};
