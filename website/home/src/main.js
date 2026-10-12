import "./style.css";

const KEY = "zunolab-lang";
const root = document.documentElement;

const titles = {
  "zh-CN": "Juce · Go 后端",
  en: "Juce · Go backend",
};

function apply(lang) {
  const next = lang === "en" ? "en" : "zh-CN";
  root.lang = next;
  document.title = titles[next];
  try {
    localStorage.setItem(KEY, next);
  } catch (e) {}
  for (const button of document.querySelectorAll("[data-set-lang]")) {
    const selected = button.dataset.setLang === "en" ? next === "en" : next !== "en";
    button.setAttribute("aria-pressed", selected ? "true" : "false");
  }
}

apply(root.lang === "en" ? "en" : "zh-CN");

document.addEventListener("click", (event) => {
  const button = event.target.closest("[data-set-lang]");
  if (!button) return;
  apply(button.dataset.setLang === "en" ? "en" : "zh-CN");
});
