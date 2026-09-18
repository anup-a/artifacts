import { apiJson } from "../client.js";
import { usage } from "../errors.js";

function quoteOf(anchor) {
  if (!anchor || typeof anchor !== "object") return "";
  return anchor.kind === "element" ? anchor.text || anchor.css || "" : anchor.quote || "";
}

function threadLines(t) {
  const out = [`${t.id}  ${t.status}  anchor ${t.anchor_status}  v${t.version}`];
  const q = quoteOf(t.anchor);
  if (q) out.push(`  on: ${q}`);
  for (const c of t.comments || []) out.push(`  ${c.author_name} (${c.author_kind}): ${c.body}`);
  return out;
}

export async function comments(cfg, { positional, flags }) {
  if (!positional[0]) throw usage("usage: byagent comments <id> [--open|--all] [--since <rfc3339>]");
  if (flags.open && flags.all) throw usage("--open and --all are mutually exclusive");
  const status = flags.all ? "all" : "open";
  const res = await apiJson(cfg, "GET", `/api/artifacts/${encodeURIComponent(positional[0])}/comments`, {
    query: { status, since: flags.since },
  });
  const threads = res.threads || [];
  return {
    json: res,
    human: threads.length ? threads.flatMap(threadLines) : [`no ${status === "all" ? "" : "open "}comments`],
  };
}

export async function reply(cfg, { positional }) {
  const [id, thread, body] = positional;
  if (!id || !thread || !body) throw usage('usage: byagent reply <id> <thread> "<text>"');
  const c = await apiJson(cfg, "POST", `/api/artifacts/${encodeURIComponent(id)}/comments/${encodeURIComponent(thread)}/reply`, {
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ body }),
  });
  return { json: c, human: [`replied to ${thread}`] };
}

export async function resolve(cfg, { positional }) {
  const [id, thread] = positional;
  if (!id || !thread) throw usage("usage: byagent resolve <id> <thread>");
  const t = await apiJson(cfg, "POST", `/api/artifacts/${encodeURIComponent(id)}/comments/${encodeURIComponent(thread)}/resolve`, {});
  return { json: t, human: [`resolved ${thread}`] };
}
