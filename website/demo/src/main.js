import "./style.css";
import { scenarios } from "./scenarios.js";

const KEY = "zunolab-lang";
const root = document.documentElement;

const ui = {
  zh: {
    title: "gg 回放",
    back: "上一步",
    next: "下一步",
    play: "播放",
    stop: "暂停",
    restart: "重来",
    yes: "y 允许",
    no: "n 拒绝",
    step: (n) => `第 ${n} 步`,
    done: "这段回放结束",
    waiting: "在等你输入 y 或 n",
    keys: "方向键逐步。遇到询问时按 y 或 n。",
  },
  en: {
    title: "gg replay",
    back: "Back",
    next: "Next",
    play: "Play",
    stop: "Pause",
    restart: "Restart",
    yes: "y allow",
    no: "n deny",
    step: (n) => `Step ${n}`,
    done: "End of this replay",
    waiting: "Waiting for y or n",
    keys: "Arrow keys step through. Press y or n at a prompt.",
  },
};

let scenario = scenarios[0];
let trail = [];
let cursor = 0;
let timer = 0;

const tabs = document.querySelector("#scenarios");
const blurb = document.querySelector("#blurb");
const screen = document.querySelector("#screen");
const caption = document.querySelector("#caption");
const progress = document.querySelector("#progress");
const choice = document.querySelector("#choice");
const backButton = document.querySelector("#back");
const nextButton = document.querySelector("#next");
const playButton = document.querySelector("#play");
const restartButton = document.querySelector("#restart");
const keys = document.querySelector("#keys");

function language() {
  return root.lang === "en" ? "en" : "zh";
}

function copy() {
  return ui[language()];
}

function text(value) {
  return value[language()];
}

function applyLanguage(lang) {
  const next = lang === "en" ? "en" : "zh-CN";
  root.lang = next;
  document.title = copy().title;
  try {
    localStorage.setItem(KEY, next === "en" ? "en" : "zh-CN");
  } catch (e) {}
  for (const button of document.querySelectorAll("[data-set-lang]")) {
    const selected = button.dataset.setLang === "en" ? next === "en" : next !== "en";
    button.setAttribute("aria-pressed", selected ? "true" : "false");
  }
  render();
}

function currentStep() {
  return scenario.steps[trail[cursor].id];
}

function canAdvance() {
  const step = currentStep();
  if (step.choice && !trail[cursor].answer) return false;
  if (cursor < trail.length - 1) return true;
  return Boolean(step.next);
}

function stop() {
  if (timer) window.clearInterval(timer);
  timer = 0;
}

function advance() {
  const step = currentStep();
  if (step.choice && !trail[cursor].answer) return false;
  if (cursor < trail.length - 1) {
    cursor += 1;
    return true;
  }
  if (!step.next) return false;
  trail.push({ id: step.next });
  cursor += 1;
  return true;
}

function choose(answer) {
  const step = currentStep();
  if (!step.choice) return;
  const nextId = answer === "y" ? step.choice.yes : step.choice.no;
  const node = trail[cursor];
  const future = trail[cursor + 1];
  if (node.answer === answer && future && future.id === nextId) {
    cursor += 1;
    render();
    return;
  }
  node.answer = answer;
  trail = trail.slice(0, cursor + 1);
  trail.push({ id: nextId });
  cursor += 1;
  render();
}

function back() {
  if (cursor === 0) return;
  stop();
  cursor -= 1;
  render();
}

function restart() {
  stop();
  trail = [{ id: scenario.start }];
  cursor = 0;
  render();
}

function play() {
  if (timer) {
    stop();
    render();
    return;
  }
  const reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  timer = window.setInterval(
    () => {
      if (!advance()) {
        stop();
        render();
        return;
      }
      render();
    },
    reduced ? 1400 : 900,
  );
  render();
}

function renderLine(line, answer) {
  const block = document.createElement("div");
  block.className = "line";
  block.dataset.tone = line.tone;
  const shown = line.prompt && answer ? `${line.text}${answer}` : line.text;
  block.textContent = shown;
  return block;
}

function render() {
  const strings = copy();
  document.title = strings.title;
  tabs.replaceChildren();
  for (const item of scenarios) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "scenario";
    button.setAttribute("role", "tab");
    button.setAttribute("aria-selected", item.id === scenario.id ? "true" : "false");
    button.textContent = text(item.title);
    button.addEventListener("click", () => {
      if (item.id === scenario.id) return;
      stop();
      scenario = item;
      trail = [{ id: scenario.start }];
      cursor = 0;
      render();
    });
    tabs.append(button);
  }

  blurb.textContent = text(scenario.blurb);
  screen.replaceChildren();
  for (let index = 0; index <= cursor; index += 1) {
    const node = trail[index];
    const step = scenario.steps[node.id];
    for (const line of step.lines) {
      screen.append(renderLine(line, node.answer));
    }
  }
  screen.scrollTop = screen.scrollHeight;

  const step = currentStep();
  caption.textContent = text(step.caption);
  const atEnd = !canAdvance() && !(step.choice && !trail[cursor].answer);
  const waiting = Boolean(step.choice && !trail[cursor].answer);
  progress.textContent = waiting ? `${strings.step(cursor + 1)} · ${strings.waiting}` : atEnd ? `${strings.step(cursor + 1)} · ${strings.done}` : strings.step(cursor + 1);

  choice.hidden = !step.choice;
  backButton.disabled = cursor === 0;
  nextButton.disabled = !canAdvance();
  backButton.textContent = strings.back;
  nextButton.textContent = strings.next;
  playButton.textContent = timer ? strings.stop : strings.play;
  playButton.setAttribute("aria-pressed", timer ? "true" : "false");
  restartButton.textContent = strings.restart;
  choice.querySelector('[data-answer="y"]').textContent = strings.yes;
  choice.querySelector('[data-answer="n"]').textContent = strings.no;
  keys.textContent = strings.keys;
}

backButton.addEventListener("click", back);
nextButton.addEventListener("click", () => {
  stop();
  if (advance()) render();
});
playButton.addEventListener("click", play);
restartButton.addEventListener("click", restart);
choice.addEventListener("click", (event) => {
  const button = event.target.closest("[data-answer]");
  if (!button) return;
  stop();
  choose(button.dataset.answer);
});

document.addEventListener("click", (event) => {
  const button = event.target.closest("[data-set-lang]");
  if (!button) return;
  applyLanguage(button.dataset.setLang === "en" ? "en" : "zh");
});

window.addEventListener("keydown", (event) => {
  const tag = event.target instanceof Element ? event.target.tagName : "";
  if (tag === "INPUT" || tag === "TEXTAREA") return;
  if (event.key === "ArrowLeft") {
    back();
    event.preventDefault();
  } else if (event.key === "ArrowRight") {
    stop();
    if (advance()) render();
    event.preventDefault();
  } else if ((event.key === "y" || event.key === "Y") && currentStep().choice) {
    stop();
    choose("y");
    event.preventDefault();
  } else if ((event.key === "n" || event.key === "N") && currentStep().choice) {
    stop();
    choose("n");
    event.preventDefault();
  }
});

trail = [{ id: scenario.start }];
cursor = 0;
applyLanguage(root.lang === "en" ? "en" : "zh");
