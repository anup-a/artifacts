import { apiJson } from "../client.js";
import { usage } from "../errors.js";

function need(positional, n, message) {
  if (positional.length < n || positional.slice(0, n).some((v) => !v)) throw usage(message);
}

// labelsOf renders "project / tags" for the human output, or "" when the
// artifact has neither.
function labelsOf(a) {
  const tags = Array.isArray(a.tags) && a.tags.length ? a.tags.map((t) => `#${t}`).join(" ") : "";
  return [a.project || "", tags].filter(Boolean).join("  ");
}

export async function get(cfg, { positional }) {
  need(positional, 1, "usage: artifacts get <id>");
  const a = await apiJson(cfg, "GET", `/api/artifacts/${encodeURIComponent(positional[0])}`);
  const labels = labelsOf(a);
  return {
    json: a,
    human: [
      `${a.id}  ${a.title || "(untitled)"}`,
      `${a.url}`,
      `version ${a.current_version} of ${a.versions_count}, ${a.open_comments} open comments${a.comments_enabled === false ? " (comments off)" : ""}`,
      ...(labels ? [labels] : []),
      `updated ${a.updated_at}`,
    ],
  };
}

export async function list(cfg, { flags }) {
  const res = await apiJson(cfg, "GET", "/api/artifacts", {
    query: { limit: flags.limit, cursor: flags.cursor, end_user: flags["end-user"], project: flags.project },
  });
  const rows = res.artifacts || [];
  return {
    json: res,
    human: rows.length
      ? rows.map((a) => {
        const labels = labelsOf(a);
        return `${a.id}  v${a.current_version}  ${a.open_comments} open  ${a.title || "(untitled)"}${labels ? `  [${labels}]` : ""}`;
      })
      : ["no artifacts"],
  };
}

export async function versions(cfg, { positional }) {
  need(positional, 1, "usage: artifacts versions <id>");
  const res = await apiJson(cfg, "GET", `/api/artifacts/${encodeURIComponent(positional[0])}/versions`);
  const rows = res.versions || [];
  return {
    json: res,
    human: rows.map((v) => `v${v.version}  ${v.files} files  ${v.bytes} bytes  ${v.comment_count} comments  ${v.created_at}`),
  };
}

export async function rollback(cfg, { positional }) {
  need(positional, 2, "usage: artifacts rollback <id> <version>");
  const [id, version] = positional;
  if (!/^\d+$/.test(version)) throw usage("version must be a positive integer");
  const res = await apiJson(cfg, "POST", `/api/artifacts/${encodeURIComponent(id)}/rollback/${version}`);
  return { json: res, human: [`rolled back to v${version}, now published as v${res.version}`, res.url] };
}

// The API answers 204, so --json still gets an object to parse.
export async function remove(cfg, { positional }) {
  need(positional, 1, "usage: artifacts delete <id>");
  const id = positional[0];
  await apiJson(cfg, "DELETE", `/api/artifacts/${encodeURIComponent(id)}`);
  return { json: { id, deleted: true }, human: [`deleted ${id}`] };
}
