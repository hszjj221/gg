import assert from "node:assert/strict";
import test from "node:test";
import {
  INTERACTIVE_BANNER,
  NOTE,
  USAGE_LINE,
  denyDangerous,
  deniedByUser,
  scenarios,
} from "../src/scenarios.js";

function joined(scenario) {
  return Object.values(scenario.steps)
    .flatMap((step) => step.lines.map((line) => line.text))
    .join("\n");
}

function byId(id) {
  const found = scenarios.find((scenario) => scenario.id === id);
  assert.ok(found, id);
  return found;
}

test("every scenario is a finite tree", () => {
  for (const scenario of scenarios) {
    const steps = scenario.steps;
    assert.ok(steps[scenario.start], scenario.id);
    const seen = new Set();
    const walk = (id, stack) => {
      assert.ok(steps[id], `${scenario.id} missing ${id}`);
      assert.equal(stack.includes(id), false, `${scenario.id} cycles at ${id}`);
      if (seen.has(id)) return;
      seen.add(id);
      const step = steps[id];
      assert.ok(step.caption.zh && step.caption.en, `${scenario.id} ${id} caption`);
      assert.ok(step.lines.length > 0, `${scenario.id} ${id} empty`);
      assert.equal(Boolean(step.next && step.choice), false, `${scenario.id} ${id} forks twice`);
      if (step.next) walk(step.next, [...stack, id]);
      if (step.choice) {
        walk(step.choice.yes, [...stack, id]);
        walk(step.choice.no, [...stack, id]);
      }
    };
    walk(scenario.start, []);
    for (const id of Object.keys(steps)) {
      assert.equal(seen.has(id), true, `${scenario.id} unreachable ${id}`);
    }
  }
});

test("read-only session never asks, and prints the usage line", () => {
  const text = joined(byId("readonly"));
  assert.equal(text.includes("Approve tool call"), false);
  assert.equal(text.includes("risk: read-only command"), true);
  assert.equal(text.includes(USAGE_LINE), true);
  assert.equal(text.includes("timeout: 2m0s"), true);
});

test("ordinary approvals use the CLI prompt and the denied-by-user result", () => {
  const text = joined(byId("approve"));
  assert.equal(text.includes(INTERACTIVE_BANNER), true);
  assert.equal(text.includes("\nApprove tool call: bash\n"), true);
  assert.equal(text.includes("\nApprove tool call: edit\n"), true);
  assert.equal(text.includes("Approve? [y/N] "), true);
  assert.equal(text.includes("risk:"), false);
  assert.equal(text.includes(deniedByUser("bash")), true);
  assert.equal(text.includes(deniedByUser("edit")), true);
  assert.equal(text.includes("applied 1 edit(s) to hello.go"), true);
  assert.equal(text.includes("exit code 1"), true);
  assert.equal(text.includes("--- before ---"), true);
  assert.equal(text.includes("--- after ---"), true);
});

test("yolo outside a terminal denies dangerous commands with the classifier reason", () => {
  const text = joined(byId("yolo"));
  assert.equal(text.includes("Approve? [y/N]"), false);
  assert.equal(text.includes(denyDangerous("bash", "rm -rf targets /")), true);
  assert.equal(text.includes(denyDangerous("bash", "piped download into a shell")), true);
  assert.equal(text.includes("rm -rf /"), true);
});

test("an interactive dangerous command asks, and memory writes do not", () => {
  const danger = joined(byId("prompt-danger"));
  assert.equal(danger.includes("risk: piped download into a shell"), true);
  assert.equal(danger.includes("Approve? [y/N] "), true);
  assert.equal(danger.includes(deniedByUser("bash")), true);
  assert.equal(danger.includes("This replay does not execute the command."), true);

  const memory = byId("memory");
  const draft = memory.steps.draft.lines.map((line) => line.text).join("\n");
  assert.equal(draft.includes("Approve tool call"), false);
  assert.equal(draft.includes(`[agent area]`), true);
  assert.equal(draft.includes(`wrote ${NOTE.length} bytes to .gg/agent/note.md`), true);
  assert.equal(draft.includes("memory added"), false);
  assert.equal(Buffer.byteLength(NOTE), NOTE.length);
  const remembered = memory.steps.memory.lines.map((line) => line.text).join("\n");
  assert.equal(remembered.includes("memory added"), true);
  assert.equal(remembered.includes("Approve tool call"), false);
  const args = `{"content":"${NOTE}","scope":"general"}`;
  assert.ok(args.length <= 120);
});
