// Spotting a deploy in an agent's tool call, so the link hook can file the
// URL in byagent without the agent doing anything. It has to stay quiet: a
// tool call counts only when the command is a deploy AND its output names a
// URL on a hosting service, so `curl x.vercel.app` or `vercel logs` file
// nothing.

// HOSTS are the hosting services whose URLs a deploy prints. Keep in step
// with internal/links (the server names the provider from the same list).
const HOSTS = [
  "vercel.app", "netlify.app", "pages.dev", "workers.dev", "github.io", "gitlab.io", "here.now",
  "fly.dev", "onrender.com", "up.railway.app", "surge.sh", "web.app", "firebaseapp.com",
  "deno.dev", "val.run", "replit.app", "lovable.app",
];

// Package runners that put the real tool one word later.
const RUNNERS = new Set(["npx", "bunx", "pnpx", "dlx", "exec", "sudo", "env", "time", "bash", "sh", "zsh"]);

// A deploy is one of these tools with a subcommand that publishes. null
// means the bare tool deploys (vercel, surge, gh-pages).
const TOOLS = {
  vercel: (sub) => sub === undefined || sub === "deploy" || /^[./~]/.test(sub),
  netlify: (sub) => sub === "deploy",
  "netlify-cli": (sub) => sub === "deploy",
  wrangler: (sub, next) => sub === "deploy" || (sub === "pages" && (next === "deploy" || next === "publish")),
  firebase: (sub) => sub === "deploy",
  fly: (sub) => sub === "deploy",
  flyctl: (sub) => sub === "deploy",
  railway: (sub) => sub === "up",
  surge: () => true,
  "gh-pages": () => true,
  deployctl: (sub) => sub === "deploy",
  render: (sub) => sub === "deploy",
};

const SCRIPT_RUNNERS = new Set(["npm", "pnpm", "yarn", "bun"]);

function words(segment) {
  return segment.trim().split(/\s+/).filter(Boolean).map((w) => w.replace(/^['"]|['"]$/g, ""));
}

// isDeployCommand reads a shell command line, segment by segment.
export function isDeployCommand(command) {
  if (typeof command !== "string") return false;
  for (const segment of command.split(/&&|\|\||;|\||\n/)) {
    const w = words(segment).filter((x) => !/^[A-Z_][A-Z0-9_]*=/.test(x));
    let i = 0;
    while (i < w.length && RUNNERS.has(w[i])) i++;
    if (w[i] === "-y" || w[i] === "--yes") i++;
    const tool = (w[i] || "").replace(/@[^/]*$/, "").split("/").pop();
    const rest = w.slice(i + 1).filter((x) => !x.startsWith("-"));
    if (TOOLS[tool] && TOOLS[tool](rest[0], rest[1])) return true;
    // npm run deploy, pnpm deploy, yarn run publish:site. A bare
    // `npm publish` ships a package, not a site, so publish needs `run`.
    if (SCRIPT_RUNNERS.has(tool)) {
      const viaRun = rest[0] === "run" || rest[0] === "run-script";
      const script = viaRun ? rest[1] : rest[0];
      if (script && /^(deploy|release)([:-][\w:-]+)?$/.test(script)) return true;
      if (viaRun && script && /^publish[:-][\w:-]+$|^publish$/.test(script)) return true;
    }
    // ./deploy.sh, scripts/publish.sh (the here.now skill's script).
    if (/(^|\/)(deploy|publish)(\.sh)?$/.test(tool)) return true;
  }
  return false;
}

const URL_RE = /https?:\/\/[A-Za-z0-9.-]+(?::\d+)?(?:\/[^\s"'<>)\]`]*)?/g;

function hostOf(u) {
  try {
    return new URL(u).hostname.toLowerCase();
  } catch {
    return "";
  }
}

const onHost = (host) => HOSTS.some((h) => host === h || host.endsWith("." + h));

// deployURL picks the one URL worth filing from a deploy's output: a URL on
// a hosting service, the stable one when the tool prints several. Vercel
// prints the per-deploy URL and then "Aliased: <stable>"; Netlify prints the
// site URL and a "Unique deploy URL" with a hash prefix. The alias, else the
// shortest host, is the address a person would share. Returns "" when the
// output names none.
export function deployURL(text) {
  if (typeof text !== "string" || !text) return "";
  const found = [];
  for (const line of text.split("\n")) {
    for (const m of line.matchAll(URL_RE)) {
      const url = m[0].replace(/[.,;:]+$/, "");
      const host = hostOf(url);
      if (!onHost(host)) continue;
      found.push({ url, host, alias: /alias|production|website url|live at|site url|primary/i.test(line), unique: /unique|preview|inspect/i.test(line) });
    }
  }
  if (!found.length) return "";
  const pool = found.filter((f) => f.alias && !f.unique);
  const pick = (pool.length ? pool : found.filter((f) => !f.unique).length ? found.filter((f) => !f.unique) : found)
    .slice()
    .sort((a, b) => a.host.length - b.host.length)[0];
  // Cloudflare Pages prints only the per-deploy address, <hash>.<project>.pages.dev;
  // <project>.pages.dev is the one that keeps working.
  const pages = pick.host.match(/^[0-9a-f]{8}\.([a-z0-9-]+\.pages\.dev)$/);
  return pages ? pick.url.replace(pick.host, pages[1]) : pick.url;
}

// outputText flattens a PostToolUse tool_response: Bash gives stdout and
// stderr, an MCP tool gives whatever JSON it returned.
export function outputText(response) {
  if (response == null) return "";
  if (typeof response === "string") return response;
  if (typeof response.stdout === "string" || typeof response.stderr === "string") {
    return `${response.stdout || ""}\n${response.stderr || ""}`;
  }
  try {
    return JSON.stringify(response).replace(/\\n/g, "\n");
  } catch {
    return "";
  }
}

// deployOf turns one PostToolUse payload into the URL to file, or "". Bash
// counts when the command is a deploy; an MCP tool counts when its name
// says it deploys or publishes (Vercel's deploy tool, a here.now server).
// byagent's own tools are skipped: those are pages already.
export function deployOf(input) {
  const name = String(input.tool_name || "");
  const ti = input.tool_input || {};
  if (name === "Bash") {
    if (!isDeployCommand(ti.command)) return "";
  } else if (!/^mcp__/.test(name) || !/deploy|publish/i.test(name) || /list|get_|search|logs?\b|status|cancel|delete|inspect/i.test(name) || /byagent|artifacts/i.test(name)) {
    return "";
  }
  return deployURL(outputText(input.tool_response));
}
