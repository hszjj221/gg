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

export default {
  extends: DefaultTheme,
  enhanceApp({ router }) {
    if (typeof window === "undefined") return;
    const run = () => {
      fitMermaid();
      window.setTimeout(fitMermaid, 80);
      window.setTimeout(fitMermaid, 500);
    };
    const previous = router.onAfterRouteChanged;
    router.onAfterRouteChanged = (to) => {
      if (typeof previous === "function") previous(to);
      run();
    };
    window.addEventListener("load", run);
  },
};
