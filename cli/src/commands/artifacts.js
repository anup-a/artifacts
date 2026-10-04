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
  need(positional, 1, "usage: byagent share <id> [--off] [--expires <when>]");
  const action = flags.off ? "revoke" : "issue";
  const body = { action };
  if (flags.expires !== undefined && !flags.off) body.expires_at = parseWhen(flags.expires);
  const a = await apiJson(cfg, "POST", `/api/artifacts/${encodeURIComponent(positional[0])}/share-code`, {
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  return {
    json: a,
    human: a.share_code
      ? [
        `code ${a.share_code}`,
        `${a.share_url}`,
        "anyone with the code can open and comment on this artifact without signing in",
        ...(a.share_code_expires_at ? [`the code stops working at ${a.share_code_expires_at}`] : []),
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
    query: {
      limit: flags.limit,
      cursor: flags.cursor,
      end_user: flags["end-user"],
      project: flags.project,
      q: flags.search,
    },
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

// versionArg reads a version number, written 3 or v3.
function versionArg(value, name = "version") {
  const m = /^v?(\d+)$/.exec(String(value || ""));
  if (!m || Number(m[1]) < 1) throw usage(`${name} must be a version number like 3 or v3`);
  return m[1];
}

export async function versions(cfg, { positional }) {
  need(positional, 1, "usage: byagent versions <id>");
  const res = await apiJson(cfg, "GET", `/api/artifacts/${encodeURIComponent(positional[0])}/versions`);
  const rows = res.versions || [];
  return {
    json: res,
    human: rows.flatMap((v) => {
      const by = [v.agent, v.model].filter(Boolean).join(" / ") || v.key || "";
      const live = v.current ? "  live" : "";
      const restored = v.restored_at ? `  restored ${v.restored_at}` : "";
      const line = `v${v.version}${live}  ${v.files} files  ${v.bytes} bytes  ${v.comment_count} comments  ${v.created_at}${by ? `  by ${by}` : ""}${restored}`;
      // preview_url opens the version for the owner only (dashboard sign-in);
      // older servers send neither field.
      const link = v.preview_url || v.url;
      return link ? [line, `    ${link}`] : [line];
    }),
  };
}

// rollback makes an old version live again. The server flips a pointer, so
// nothing is uploaded and access, share code, expiry and comments stay as
// they are.
export async function rollback(cfg, { positional }) {
  need(positional, 2, "usage: byagent rollback <id> <version>");
  const [id] = positional;
  const version = versionArg(positional[1]);
  const res = await apiJson(cfg, "POST", `/api/artifacts/${encodeURIComponent(id)}/rollback/${version}`);
  const was = res.previous_version ? ` (was v${res.previous_version})` : "";
  return { json: res, human: [`v${res.version} is live${was}`, res.url] };
}

// version rm deletes one old version for good. The live one cannot be
// deleted; restore another first.
export async function version(cfg, { positional }) {
  const [sub, id, n] = positional;
  if (sub !== "rm" || !id || !n) throw usage("usage: byagent version rm <id> <version>");
  const res = await apiJson(cfg, "DELETE", `/api/artifacts/${encodeURIComponent(id)}/versions/${versionArg(n)}`);
  const orphaned = res.orphaned_comments ? `; ${res.orphaned_comments} comments on it are kept, marked orphaned` : "";
  return { json: res, human: [`deleted v${res.deleted_version} of ${res.id}${orphaned}`, `${res.versions_count} versions left, v${res.current_version} live`] };
}

// diff lists the files added, removed and changed between two versions; to
// defaults to the live one.
export async function diff(cfg, { positional }) {
  need(positional, 2, "usage: byagent diff <id> <from> [<to>]");
  const query = { from: versionArg(positional[1], "from") };
  if (positional[2]) query.to = versionArg(positional[2], "to");
  const res = await apiJson(cfg, "GET", `/api/artifacts/${encodeURIComponent(positional[0])}/diff`, { query });
  const lines = [
    ...res.added.map((f) => `+ ${f.path}  ${f.size} bytes`),
    ...res.removed.map((f) => `- ${f.path}  ${f.size} bytes`),
    ...res.changed.map((f) => `~ ${f.path}  ${f.from_size} -> ${f.to_size} bytes`),
  ];
  return {
    json: res,
    human: [`v${res.from} -> v${res.to}: ${res.added.length} added, ${res.removed.length} removed, ${res.changed.length} changed, ${res.unchanged} unchanged`, ...lines],
  };
}

// The API answers 204, so --json still gets an object to parse.
export async function remove(cfg, { positional }) {
  need(positional, 1, "usage: byagent delete <id>");
  const id = positional[0];
  await apiJson(cfg, "DELETE", `/api/artifacts/${encodeURIComponent(id)}`);
  return { json: { id, deleted: true }, human: [`deleted ${id}`] };
}

const UNITS = { m: 60e3, h: 3600e3, d: 86400e3, w: 7 * 86400e3 };

// parseWhen reads an --expires value: a duration from now (30m, 12h, 7d, 2w),
// an RFC 3339 time, or never/off for no end. It returns an ISO string or null.
export function parseWhen(value, now = Date.now()) {
  const v = String(value).trim();
  if (v === "never" || v === "off") return null;
  const m = /^(\d+)([mhdw])$/.exec(v);
  if (m) return new Date(now + Number(m[1]) * UNITS[m[2]]).toISOString();
  const t = Date.parse(v);
  if (Number.isNaN(t) || !/\d{4}-\d{2}-\d{2}T/.test(v)) {
    throw usage(`--expires takes a duration (30m, 12h, 7d, 2w), an RFC 3339 time, or never; got ${v}`);
  }
  return new Date(t).toISOString();
}

// setExpiry is shared by expire and publish --expires.
export async function setExpiry(cfg, id, when) {
  return apiJson(cfg, "POST", `/api/artifacts/${encodeURIComponent(id)}/expiry`, {
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ expires_at: when }),
  });
}

export async function expire(cfg, { positional }) {
  need(positional, 2, "usage: byagent expire <id> <30m|12h|7d|2w|rfc3339|never>");
  const when = parseWhen(positional[1]);
  const a = await setExpiry(cfg, positional[0], when);
  return { json: a, human: [when ? `${a.id} stops working at ${a.expires_at}, then is erased` : `${a.id} no longer expires`] };
}
