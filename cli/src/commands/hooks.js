import { existsSync, mkdirSync, readFileSync, writeFileSync, renameSync } from "node:fs";
import { homedir, tmpdir } from "node:os";
import { basename, dirname, extname, isAbsolute, join, relative, resolve, sep } from "node:path";
import { LINK_FILE } from "../files.js";
import { usage } from "../errors.js";
import { resolveConfig } from "../config.js";
import { clientHeaders } from "../agent.js";
import { deployOf } from "../deploys.js";
import { linkBody } from "./links.js";

const USAGE = "usage: byagent hooks install|uninstall claude|codex [--scope user|project]";

// The command both agents run after an edit. It has to be on PATH: `npx byagent`
// would add a package resolve to every file the agent touches.
const HOOK_COMMAND = "byagent hooks run";

// Claude Code edits with Write/Edit/MultiEdit; Codex edits with apply_patch.
const MATCHERS = { claude: "Write|Edit|MultiEdit", codex: "apply_patch" };

// The link hook files what an agent deploys elsewhere. Claude Code only: its
// Bash and MCP payloads are the ones deployOf reads.
const LINK_COMMAND = "byagent hooks link";
const LINK_MATCHERS = { claude: "Bash|mcp__.*" };

// A hook runs inside the agent's tool call, so the request gets a short leash;
// a slow or down server costs one missing link, never a stuck agent.
const LINK_TIMEOUT_MS = 4000;

const PAGE_EXTS = new Set([".html", ".htm", ".md", ".markdown"]);

// Files an agent edits all day that are not pages anyone reads as one.
// brief.md has its own command, brief push, and is published on purpose.
const SKIP_NAMES = new Set([
  "readme.md", "changelog.md", "license.md", "contributing.md", "security.md", "code_of_conduct.md",
  "claude.md", "agents.md", "skill.md", "memory.md", "brief.md",
]);

// Code trees: HTML here is an app's template, not an artifact.
const SKIP_DIRS = new Set([
  ".git", "node_modules", ".claude", ".codex", ".github", "vendor", "dist", "build", "out", ".next",
  "coverage", "src", "public", "static", "templates", "assets",
]);

// One session gets a few nudges at most; after that the agent has the idea.
const MAX_NUDGES = 5;

// editedPaths returns the absolute paths a tool call wrote. Claude Code names
// the file in tool_input.file_path; Codex sends the whole patch, whose headers
// name each added, updated or moved-to file.
export function editedPaths(input) {
  const cwd = input.cwd || process.cwd();
  const ti = input.tool_input || {};
  const raw = [];
  if (typeof ti.file_path === "string") raw.push(ti.file_path);
  if (input.tool_name === "apply_patch" && typeof ti.command === "string") {
    for (const m of ti.command.matchAll(/^\*\*\* (?:Add File|Update File|Move to): (.+)$/gm)) raw.push(m[1].trim());
  }
  return [...new Set(raw.map((p) => (isAbsolute(p) ? p : resolve(cwd, p))))];
}

// Directory names are checked below the working directory only, so a project
// that happens to live under ~/build is not skipped wholesale.
export function isPageCandidate(path, { home = homedir(), cwd } = {}) {
  if (!PAGE_EXTS.has(extname(path).toLowerCase())) return false;
  if (SKIP_NAMES.has(basename(path).toLowerCase())) return false;
  for (const agentHome of [".claude", ".codex", ".artifacts"]) {
    if (path.startsWith(join(home, agentHome) + sep)) return false;
  }
  const rel = cwd ? relative(resolve(cwd), dirname(path)) : "";
  const inside = cwd && (rel === "" || (!rel.startsWith("..") && !isAbsolute(rel)));
  const below = inside ? rel : dirname(path);
  return !below.split(sep).some((seg) => SKIP_DIRS.has(seg));
}

// findLink looks for the .artifacts.json that binds a published directory,
// from the file's own directory up to the session's working directory.
export function findLink(path, cwd) {
  const top = cwd ? resolve(cwd) : null;
  let dir = dirname(path);
  for (;;) {
    try {
      const link = JSON.parse(readFileSync(join(dir, LINK_FILE), "utf8"));
      if (link && link.id) return { dir, id: link.id };
    } catch {}
    const up = dirname(dir);
    if (!top || dir === top || up === dir || relative(top, up).startsWith("..")) return null;
    dir = up;
  }
}

export function nudgeText(path, cwd, link) {
  const show = (p) => {
    const rel = cwd ? relative(cwd, p) : p;
    return rel && !rel.startsWith("..") ? rel : p;
  };
  if (link) {
    const dir = show(link.dir) || ".";
    return `byagent: ${show(path)} belongs to the published artifact ${link.id}. When you have finished editing, republish with \`byagent publish ${dir} --json\` so the link shows this version, then check \`byagent comments ${link.id} --open --json\`.`;
  }
  return `byagent: ${show(path)} could be shared as a page. If it is something to read rather than part of a codebase, publish it when you finish this turn, drafts included: \`byagent publish ${show(path)} --project "<name>" --tag <t> --json\`, with --private if it is internal. Then give the user the returned url and say you published it.`;
}

// noteText is the one line the user sees, so a link in the reply never
// arrives without a trace of what suggested it.
export function noteText(path, cwd, link) {
  const rel = cwd ? relative(cwd, path) : path;
  const shown = rel && !rel.startsWith("..") ? rel : path;
  return link ? `byagent: suggested republishing ${shown}` : `byagent: suggested publishing ${shown}`;
}

// Per-session memory of which files were already nudged, so the reminder
// comes once per file, not once per edit.
function stateFile(session, dir) {
  return join(dir, `${String(session).replace(/[^A-Za-z0-9_-]/g, "_") || "default"}.json`);
}

function loadSeen(session, dir) {
  try {
    const parsed = JSON.parse(readFileSync(stateFile(session, dir), "utf8"));
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

function saveSeen(session, dir, seen) {
  mkdirSync(dir, { recursive: true });
  const path = stateFile(session, dir);
  writeFileSync(`${path}.tmp`, JSON.stringify(seen));
  renameSync(`${path}.tmp`, path);
}

// nudge turns one PostToolUse payload into the hook's stdout object, or null.
export function nudge(input, { home = homedir(), stateDir = join(tmpdir(), "byagent-hooks") } = {}) {
  const session = input.session_id || "default";
  const seen = loadSeen(session, stateDir);
  if (seen.length >= MAX_NUDGES) return null;
  const lines = [];
  const notes = [];
  for (const path of editedPaths(input)) {
    if (seen.length >= MAX_NUDGES) break;
    if (seen.includes(path) || !isPageCandidate(path, { home, cwd: input.cwd })) continue;
    seen.push(path);
    const link = findLink(path, input.cwd);
    lines.push(nudgeText(path, input.cwd, link));
    notes.push(noteText(path, input.cwd, link));
  }
  if (!lines.length) return null;
  saveSeen(session, stateDir, seen);
  return {
    systemMessage: notes.join("\n"),
    hookSpecificOutput: { hookEventName: "PostToolUse", additionalContext: lines.join("\n") },
  };
}

async function readStdin() {
  const chunks = [];
  for await (const chunk of process.stdin) chunks.push(chunk);
  return Buffer.concat(chunks).toString("utf8");
}

// run is what the agent invokes. It never fails the tool call: a broken
// nudge prints nothing and exits 0.
async function run(env) {
  if (env.BYAGENT_NUDGE === "0") return { json: {}, human: [] };
  try {
    const out = nudge(JSON.parse(await readStdin()));
    return out ? { json: out, human: [JSON.stringify(out)] } : { json: {}, human: [] };
  } catch {
    return { json: {}, human: [] };
  }
}

// fileDeploy turns one PostToolUse payload into the hook's stdout object, or
// null when the call was not a deploy, nothing is configured, or the server
// did not answer. A new link gets one line for the user and one for the
// agent; a deploy refreshing a link already filed says nothing.
export async function fileDeploy(input, { env = process.env, fetchImpl = fetch, config, home } = {}) {
  const url = deployOf(input);
  if (!url) return null;
  let cfg = config;
  if (!cfg) {
    try {
      cfg = resolveConfig(home ? { env, home } : { env });
    } catch {
      return null;
    }
  }
  const res = await fetchImpl(`${cfg.api}/api/links`, {
    method: "POST",
    headers: { ...clientHeaders(env), Authorization: `Bearer ${cfg.token}`, "Content-Type": "application/json" },
    body: JSON.stringify(linkBody(url, {}, env, input.cwd || process.cwd())),
    signal: AbortSignal.timeout(LINK_TIMEOUT_MS),
  });
  if (!res.ok) return null;
  const l = await res.json();
  if (!l.created) return null;
  return {
    systemMessage: `byagent: linked ${l.url}`,
    hookSpecificOutput: {
      hookEventName: "PostToolUse",
      additionalContext: `byagent: filed ${l.url} in the user's byagent dashboard as a link (${l.provider}; byagent does not host it). Nothing to do; to give it a title run \`byagent link ${l.url} --title "<title>"\`.`,
    },
  };
}

async function runLink(env) {
  if (env.BYAGENT_LINK === "0") return { json: {}, human: [] };
  try {
    const out = await fileDeploy(JSON.parse(await readStdin()), { env });
    return out ? { json: out, human: [JSON.stringify(out)] } : { json: {}, human: [] };
  } catch {
    return { json: {}, human: [] };
  }
}

export function settingsPath(agent, { project = false, home = homedir(), cwd = process.cwd() } = {}) {
  if (agent === "claude") return project ? join(cwd, ".claude", "settings.json") : join(home, ".claude", "settings.json");
  if (agent === "codex") return project ? join(cwd, ".codex", "hooks.json") : join(home, ".codex", "hooks.json");
  throw usage(USAGE);
}

function readSettings(path) {
  if (!existsSync(path)) return {};
  const parsed = JSON.parse(readFileSync(path, "utf8"));
  return parsed && typeof parsed === "object" ? parsed : {};
}

const isOurs = (h) => h && typeof h.command === "string" && (h.command.startsWith(HOOK_COMMAND) || h.command.startsWith(LINK_COMMAND));

// withoutOurs drops our command from every PostToolUse group and drops groups
// it leaves empty; the user's other hooks stay exactly as they were.
function withoutOurs(groups) {
  return (groups || [])
    .map((g) => ({ ...g, hooks: (g.hooks || []).filter((h) => !isOurs(h)) }))
    .filter((g) => g.hooks.length);
}

export function installInto(settings, agent) {
  const hooks = { ...(settings.hooks || {}) };
  hooks.PostToolUse = [...withoutOurs(hooks.PostToolUse), { matcher: MATCHERS[agent], hooks: [{ type: "command", command: HOOK_COMMAND }] }];
  if (LINK_MATCHERS[agent]) hooks.PostToolUse.push({ matcher: LINK_MATCHERS[agent], hooks: [{ type: "command", command: LINK_COMMAND }] });
  return { ...settings, hooks };
}

export function uninstallFrom(settings) {
  if (!settings.hooks || !settings.hooks.PostToolUse) return settings;
  const hooks = { ...settings.hooks, PostToolUse: withoutOurs(settings.hooks.PostToolUse) };
  if (!hooks.PostToolUse.length) delete hooks.PostToolUse;
  return { ...settings, hooks };
}

export async function hooks(_cfg, { positional, flags, env = process.env }) {
  const [action, agent] = positional;
  if (action === "run") return run(env);
  if (action === "link") return runLink(env);
  if (!["install", "uninstall"].includes(action) || !MATCHERS[agent]) throw usage(USAGE);

  const scope = flags.scope || "user";
  if (!["user", "project"].includes(scope)) throw usage(USAGE);
  const path = settingsPath(agent, { project: scope === "project" });
  const before = readSettings(path);
  const after = action === "install" ? installInto(before, agent) : uninstallFrom(before);
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, JSON.stringify(after, null, 2) + "\n");

  const human = [`${action === "install" ? "installed" : "removed"} the byagent PostToolUse hooks in ${path}`];
  if (action === "install") {
    human.push("It suggests publishing .html and .md pages the agent writes. It never publishes on its own.");
    if (LINK_MATCHERS[agent]) human.push("When the agent deploys somewhere else (Vercel, Netlify, Cloudflare Pages, GitHub Pages, here.now ...), it files the URL in the dashboard as a link. BYAGENT_LINK=0 turns that off.");
    if (agent === "codex") human.push("Codex runs a new hook only after you trust it: open /hooks in Codex.");
    human.push("The hook runs `byagent hooks run`, so byagent must be on PATH: npm install -g byagent");
  }
  return { json: { ok: true, agent, action, settings: path }, human };
}
