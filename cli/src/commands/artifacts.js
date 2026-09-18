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

// share issues, rotates or withdraws an artifact's six-digit share code.
// Issuing and rotating are the same request, which is what makes rotation a
// revocation: every link carrying the old code stops working.
export async function share(cfg, { positional, flags }) {
  need(positional, 1, "usage: byagent share <id> [--off]");
  const action = flags.off ? "revoke" : "issue";
  const a = await apiJson(cfg, "POST", `/api/artifacts/${encodeURIComponent(positional[0])}/share-code`, {
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ action }),
  });
  return {
    json: a,
    human: a.share_code
      ? [
        `code ${a.share_code}`,
        `${a.share_url}`,
        "anyone with the code can open and comment on this artifact without signing in",
      ]
      : [`${a.id} is no longer shared with a code`],
  };
}

export async function get(cfg, { positional }) {
  need(positional, 1, "usage: byagent get <id>");
  const a = await apiJson(cfg, "GET", `/api/artifacts/${encodeURIComponent(positional[0])}`);
  const labels = labelsOf(a);
  return {
    json: a,
    human: [
      `${a.id}  ${a.title || "(untitled)"}`,
      `${a.url}`,
      `visibility ${a.visibility || "public"}${a.share_code ? `, shared with code ${a.share_code}` : ""}`,
      ...(a.share_url ? [`share link ${a.share_url}`] : []),
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
        return `${a.id}  v${a.current_version}  ${a.open_comments} open  ${a.title || "(untitled)"}${a.visibility === "private" ? "  [private]" : ""}${labels ? `  [${labels}]` : ""}`;
      })
      : ["no artifacts"],
  };
}

export async function versions(cfg, { positional }) {
  need(positional, 1, "usage: byagent versions <id>");
  const res = await apiJson(cfg, "GET", `/api/artifacts/${encodeURIComponent(positional[0])}/versions`);
  const rows = res.versions || [];
  return {
    json: res,
    human: rows.map((v) => `v${v.version}  ${v.files} files  ${v.bytes} bytes  ${v.comment_count} comments  ${v.created_at}`),
  };
}

export async function rollback(cfg, { positional }) {
  need(positional, 2, "usage: byagent rollback <id> <version>");
  const [id, version] = positional;
  if (!/^\d+$/.test(version)) throw usage("version must be a positive integer");
  const res = await apiJson(cfg, "POST", `/api/artifacts/${encodeURIComponent(id)}/rollback/${version}`);
  return { json: res, human: [`rolled back to v${version}, now published as v${res.version}`, res.url] };
}

// The API answers 204, so --json still gets an object to parse.
export async function remove(cfg, { positional }) {
  need(positional, 1, "usage: byagent delete <id>");
  const id = positional[0];
  await apiJson(cfg, "DELETE", `/api/artifacts/${encodeURIComponent(id)}`);
  return { json: { id, deleted: true }, human: [`deleted ${id}`] };
}
