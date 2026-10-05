import { apiJson } from "../client.js";
import { usage } from "../errors.js";

const enc = encodeURIComponent;

function post(cfg, path, body) {
  return apiJson(cfg, "POST", path, {
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
}

// recordLines prints the DNS records a pending domain needs, one per line.
function recordLines(d) {
  return (d.dns_instructions || []).map((r) => `  ${r.type.padEnd(5)}  ${r.host.padEnd(24)}  ${r.value}`);
}

function domainLines(d) {
  const lines = [`${d.domain}  ${d.status}${d.ssl_status ? `  (certificate: ${d.ssl_status})` : ""}`, d.url];
  if (d.status !== "active") {
    lines.push("add these DNS records at your DNS provider:", ...recordLines(d));
    if (d.verification) lines.push(d.verification.message);
  }
  const mounts = d.mounts || [];
  lines.push(
    ...(mounts.length
      ? mounts.map((m) => `serves ${m.artifact_id} at ${m.url}${m.primary ? "  (primary)" : ""}`)
      : ["no pages yet: byagent domain add " + d.domain + " --page <id> [--path /x]"]),
  );
  return lines;
}

// domains lists the workspace's custom domains.
export async function domains(cfg) {
  const res = await apiJson(cfg, "GET", "/api/domains");
  const rows = res.domains || [];
  return {
    json: res,
    human: rows.length
      ? rows.map((d) => `${d.domain}  ${d.status}  ${(d.mounts || []).length} page${(d.mounts || []).length === 1 ? "" : "s"}`)
      : ["no custom domains; byagent domain add <host> connects one"],
  };
}

// domain dispatches the subcommands; a bare hostname shows its status.
export async function domain(cfg, ctx) {
  const [sub, ...rest] = ctx.positional;
  switch (sub) {
    case "add":
      return add(cfg, { ...ctx, positional: rest });
    case "rm":
    case "remove":
      return remove(cfg, { ...ctx, positional: rest });
    case "unmount":
      return unmount(cfg, { ...ctx, positional: rest });
    case "primary":
      return primary(cfg, { ...ctx, positional: rest });
    case undefined:
    case "":
      throw usage("usage: byagent domain <host> | add <host> [--page <id>] [--path /x] [--primary] | rm <host> | unmount <host> [--path /x] | primary <id> <host>|--off");
    default:
      return show(cfg, sub);
  }
}

async function show(cfg, host) {
  const d = await apiJson(cfg, "GET", `/api/domains/${enc(host)}`);
  return { json: d, human: domainLines(d) };
}

// add connects a hostname (repeating it is harmless) and, with --page,
// serves that page at --path on it. --primary also makes byagent.dev links
// to the page redirect there, once the domain is active.
async function add(cfg, { positional, flags }) {
  const host = positional[0];
  if (!host) throw usage("usage: byagent domain add <host> [--page <id>] [--path /x] [--primary]");
  if (flags.path !== undefined && !flags.page) throw usage("--path needs --page");
  if (flags.primary && !flags.page) throw usage("--primary needs --page");
  let d = await post(cfg, "/api/domains", { domain: host });
  const notes = [];
  if (flags.page) {
    await post(cfg, `/api/domains/${enc(d.domain)}/mounts`, { path: flags.path || "/", artifact_id: flags.page });
    if (flags.primary) {
      if (d.status === "active") {
        await post(cfg, `/api/artifacts/${enc(flags.page)}/primary-domain`, { domain: d.domain, path: flags.path || "/" });
      } else {
        notes.push(`not primary yet: the domain is ${d.status}. Once it is active run: byagent domain primary ${flags.page} ${d.domain}${flags.path ? ` --path ${flags.path}` : ""}`);
      }
    }
    d = await apiJson(cfg, "GET", `/api/domains/${enc(d.domain)}`);
  }
  return { json: notes.length ? { ...d, note: notes.join(" ") } : d, human: [...domainLines(d), ...notes] };
}

// The API answers 204, so --json still gets an object to parse.
async function remove(cfg, { positional }) {
  const host = positional[0];
  if (!host) throw usage("usage: byagent domain rm <host>");
  await apiJson(cfg, "DELETE", `/api/domains/${enc(host)}`);
  return { json: { domain: host, removed: true }, human: [`${host} is disconnected; its pages keep their byagent.dev links`] };
}

async function unmount(cfg, { positional, flags }) {
  const host = positional[0];
  if (!host) throw usage("usage: byagent domain unmount <host> [--path /x]");
  const path = flags.path || "/";
  await apiJson(cfg, "DELETE", `/api/domains/${enc(host)}/mounts`, { query: { path } });
  return { json: { domain: host, path, unmounted: true }, human: [`nothing is served at ${host}${path} now`] };
}

async function primary(cfg, { positional, flags }) {
  const [id, host] = positional;
  if (!id || (!host && !flags.off)) throw usage("usage: byagent domain primary <id> <host> [--path /x] | byagent domain primary <id> --off");
  const body = flags.off ? { domain: null } : { domain: host };
  if (!flags.off && flags.path !== undefined) body.path = flags.path;
  const res = await post(cfg, `/api/artifacts/${enc(id)}/primary-domain`, body);
  return {
    json: res,
    human: res.primary_url
      ? [`byagent.dev links to ${id} now redirect to ${res.primary_url}`]
      : res.primary
        ? [`${id} redirects to ${res.primary.url} while the page is public or shared by code`]
        : [`${id} no longer redirects to a custom domain`],
  };
}
