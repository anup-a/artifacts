import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, join, resolve } from "node:path";

// The block init writes. An agent that has never heard of byagent reads it
// from the instructions file it already loads, so "publish this as a byagent
// page" works without a web search, which does not find byagent.dev yet.
const START = "<!-- byagent -->";
const END = "<!-- /byagent -->";
export const BLOCK = `${START}
byagent: to turn a document, plan, report or HTML page into a shareable link people can comment on, run \`npx -y byagent@latest publish <file-or-dir> --json\` and hand back the \`url\` it prints. Instructions: https://byagent.dev/skill.md
${END}`;

// writeBlock adds the block to a file, or replaces an earlier one in place,
// and reports what it did. Text outside the markers is never touched.
export function writeBlock(path) {
  const before = existsSync(path) ? readFileSync(path, "utf8") : null;
  let after;
  if (before === null) after = BLOCK + "\n";
  else {
    const i = before.indexOf(START);
    const j = i < 0 ? -1 : before.indexOf(END, i);
    if (i >= 0 && j >= 0) after = before.slice(0, i) + BLOCK + before.slice(j + END.length);
    else after = before + (before === "" || before.endsWith("\n\n") ? "" : before.endsWith("\n") ? "\n" : "\n\n") + BLOCK + "\n";
  }
  if (after === before) return { path, action: "unchanged" };
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, after);
  return { path, action: before === null ? "created" : "updated" };
}

// targets picks the files. In a project: AGENTS.md (created if missing), and
// CLAUDE.md only when it exists and does not already pull in AGENTS.md.
// --global: the user-level files of Claude Code and Codex, for agents that
// are installed (their directory exists).
export function targets({ global, cwd = process.cwd(), home = homedir() }) {
  if (global) {
    return [
      { dir: join(home, ".claude"), file: join(home, ".claude", "CLAUDE.md") },
      { dir: join(home, ".codex"), file: join(home, ".codex", "AGENTS.md") },
    ].map(({ dir, file }) => (existsSync(dir) ? { file } : { file, skip: `${dir} does not exist` }));
  }
  const out = [{ file: resolve(cwd, "AGENTS.md") }];
  const claude = resolve(cwd, "CLAUDE.md");
  if (existsSync(claude)) {
    out.push(/^@AGENTS\.md\s*$/m.test(readFileSync(claude, "utf8")) ? { file: claude, skip: "it already includes AGENTS.md" } : { file: claude });
  }
  return out;
}

export async function init(_cfg, { flags, home, cwd }) {
  const files = targets({ global: Boolean(flags.global), cwd, home }).map((t) =>
    t.skip ? { path: t.file, action: "skipped", reason: t.skip } : writeBlock(t.file));
  return {
    json: { ok: true, files },
    human: files.map((f) => `${f.action.padEnd(9)} ${f.path}${f.reason ? ` (${f.reason})` : ""}`),
  };
}
