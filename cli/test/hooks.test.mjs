import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { editedPaths, isPageCandidate, findLink, nudge, installInto, uninstallFrom } from "../src/commands/hooks.js";

function tmp() {
  return mkdtempSync(join(tmpdir(), "byagent-hooks-"));
}

const context = (out) => out.hookSpecificOutput.additionalContext;

test("editedPaths reads Claude's file_path and every file named in a Codex patch", () => {
  assert.deepEqual(editedPaths({ cwd: "/w", tool_name: "Write", tool_input: { file_path: "/w/plan.md" } }), ["/w/plan.md"]);
  const patch = "*** Begin Patch\n*** Add File: report.html\n+<h1>x</h1>\n*** Update File: a/old.md\n*** Move to: a/new.md\n*** End Patch";
  assert.deepEqual(editedPaths({ cwd: "/w", tool_name: "apply_patch", tool_input: { command: patch } }), [
    "/w/report.html",
    "/w/a/old.md",
    "/w/a/new.md",
  ]);
  assert.deepEqual(editedPaths({ cwd: "/w", tool_name: "Bash", tool_input: { command: "*** Add File: x.md" } }), []);
});

test("isPageCandidate keeps pages and skips code, agent config and repo docs", () => {
  const home = "/h";
  const cwd = "/h/code/app";
  const ok = (p) => isPageCandidate(p, { home, cwd });
  assert.ok(ok("/h/code/app/report.html"));
  assert.ok(ok("/h/code/app/docs/plan.md"));
  assert.ok(!ok("/h/code/app/main.go"));
  assert.ok(!ok("/h/code/app/README.md"));
  assert.ok(!ok("/h/code/app/CLAUDE.md"));
  assert.ok(!ok("/h/code/app/src/index.html"));
  assert.ok(!ok("/h/code/app/node_modules/x/readme.markdown"));
  assert.ok(!ok("/h/.claude/plans/p.md"));
  // Only directories below the working directory count as code trees.
  assert.ok(isPageCandidate("/h/build/site/index.html", { home, cwd: "/h/build/site" }));
});

test("findLink finds the binding directory up to the working directory", () => {
  const root = tmp();
  mkdirSync(join(root, "site", "pages"), { recursive: true });
  writeFileSync(join(root, "site", ".artifacts.json"), JSON.stringify({ id: "ar_1", api: "https://a" }));
  assert.deepEqual(findLink(join(root, "site", "pages", "a.html"), root), { dir: join(root, "site"), id: "ar_1" });
  assert.equal(findLink(join(root, "other.md"), root), null);
});

test("nudge speaks once per file, points bound files at a republish, and stays quiet otherwise", () => {
  const cwd = tmp();
  const stateDir = tmp();
  mkdirSync(join(cwd, "site"));
  writeFileSync(join(cwd, "site", ".artifacts.json"), JSON.stringify({ id: "ar_9" }));
  const write = (p) => ({ session_id: "s1", cwd, tool_name: "Write", tool_input: { file_path: join(cwd, p) } });

  const first = nudge(write("plan.md"), { home: "/nohome", stateDir });
  assert.match(context(first), /plan\.md could be shared/);
  assert.match(context(first), /byagent publish plan\.md --project/);
  assert.equal(first.hookSpecificOutput.hookEventName, "PostToolUse");
  assert.equal(nudge(write("plan.md"), { home: "/nohome", stateDir }), null);

  const bound = nudge(write("site/index.html"), { home: "/nohome", stateDir });
  assert.match(context(bound), /published artifact ar_9/);
  assert.match(context(bound), /byagent publish site --json/);

  assert.equal(nudge(write("main.go"), { home: "/nohome", stateDir }), null);
});

test("nudge stops after five files in a session", () => {
  const cwd = tmp();
  const stateDir = tmp();
  const said = [];
  for (let i = 0; i < 7; i++) {
    said.push(nudge({ session_id: "s2", cwd, tool_name: "Write", tool_input: { file_path: join(cwd, `p${i}.md`) } }, { home: "/nohome", stateDir }));
  }
  assert.equal(said.filter(Boolean).length, 5);
});

test("install is idempotent and uninstall removes only the byagent hook", () => {
  const theirs = { matcher: "Write", hooks: [{ type: "command", command: "prettier --write" }] };
  const settings = { model: "x", hooks: { PostToolUse: [theirs], Stop: [{ hooks: [{ type: "command", command: "s.sh" }] }] } };
  const once = installInto(settings, "claude");
  const twice = installInto(once, "claude");
  assert.deepEqual(twice, once);
  assert.equal(once.hooks.PostToolUse.length, 2);
  assert.deepEqual(once.hooks.PostToolUse[1], { matcher: "Write|Edit|MultiEdit", hooks: [{ type: "command", command: "byagent hooks run" }] });
  assert.equal(installInto({}, "codex").hooks.PostToolUse[0].matcher, "apply_patch");

  const back = uninstallFrom(twice);
  assert.deepEqual(back, settings);
  assert.deepEqual(uninstallFrom(installInto({}, "claude")), { hooks: {} });
});
