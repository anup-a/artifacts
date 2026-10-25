import { apiJson } from "../client.js";
import { usage } from "../errors.js";
import { findProject } from "../project.js";
import { detectAgent } from "../agent.js";

const LINK_USAGE = 'usage: byagent link <url> [--title "<t>"] [--project <name>] [--agent <name>] [--model <name>]';
const LINKS_USAGE = "usage: byagent links [--search <text>] [--project <name>]";
const UNLINK_USAGE = "usage: byagent unlink <id|url>";

// linkBody is what POST /api/links gets. The project falls back to the
// folder's .byagent.json collection, the same default publish uses.
export function linkBody(url, flags, env = process.env, cwd = process.cwd()) {
  const body = { url: String(url) };
  // Same order as publish: --agent / --model, then BYAGENT_*, then detected.
  const agent = flags.agent || detectAgent(env);
  const model = flags.model || env.BYAGENT_MODEL || "";
  if (agent) body.agent = String(agent).slice(0, 64);
  if (model) body.model = String(model).slice(0, 64);
  if (flags.title !== undefined) body.title = String(flags.title);
  let project = flags.project;
  if (project === undefined) {
    try {
      project = findProject(cwd)?.collection;
    } catch {}
  }
  if (project) body.project = String(project);
  return body;
}

const shown = (l) => `${l.id}  ${l.provider}  ${l.url}${l.title ? `  "${l.title}"` : ""}`;

// link files work published somewhere else (Vercel, Netlify, GitHub Pages,
// here.now ...) in the workspace, next to the pages byagent hosts. byagent
// keeps the URL and the labels, never a copy. The same URL again refreshes
// the link, so running it after every deploy is fine.
export async function link(cfg, { positional, flags, env = process.env }) {
  const [url, ...extra] = positional;
  if (!url || extra.length) throw usage(LINK_USAGE);
  const res = await apiJson(cfg, "POST", "/api/links", {
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(linkBody(url, flags, env)),
  });
  return { json: res, human: [`${res.created ? "linked" : "updated"} ${shown(res)}`, "It shows in the dashboard under Linked; byagent does not host it."] };
}

export async function links(cfg, { positional, flags }) {
  if (positional.length) throw usage(LINKS_USAGE);
  const res = await apiJson(cfg, "GET", "/api/links", { query: { q: flags.search, project: flags.project } });
  const rows = res.links || [];
  return { json: res, human: rows.length ? rows.map(shown) : ["no links"] };
}

export async function unlink(cfg, { positional }) {
  const [ref, ...extra] = positional;
  if (!ref || extra.length) throw usage(UNLINK_USAGE);
  await apiJson(cfg, "DELETE", `/api/links/${encodeURIComponent(ref)}`);
  return { json: { ok: true, removed: ref }, human: [`removed ${ref} (the page itself is untouched)`] };
}
