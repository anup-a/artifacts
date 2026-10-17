import { apiJson } from "../client.js";
import { usage } from "../errors.js";

// table pads every column but the last to its widest cell, two spaces apart.
function table(rows) {
  const widths = rows[0].map((_, i) => Math.max(...rows.map((r) => String(r[i]).length)));
  return rows.map((r) => r.map((cell, i) => (i === r.length - 1 ? String(cell) : String(cell).padEnd(widths[i]))).join("  "));
}

// collections lists every project label of the workspace as a collection,
// most recently active first. --names keeps only what an agent needs to reuse
// a label exactly: the full listing carries each collection's latest pages.
export async function collections(cfg, { flags = {} } = {}) {
  const res = await apiJson(cfg, "GET", "/api/collections");
  const rows = res.collections || [];
  if (flags.names) {
    return {
      json: { collections: rows.map((c) => ({ name: c.name, count: c.count, updated_at: c.updated_at })) },
      human: rows.length ? rows.map((c) => c.name) : ["no collections"],
    };
  }
  return {
    json: res,
    human: rows.length
      ? table([
        ["NAME", "COUNT", "UPDATED", "SHARED"],
        ...rows.map((c) => [c.name, c.count, c.updated_at, c.share_url || "-"]),
      ])
      : ["no collections; publish with --project <name> to start one"],
  };
}

// collection shows one collection's artifacts in reading order: its saved
// order first, then by publish time.
export async function collection(cfg, { positional }) {
  if (!positional[0]) throw usage("usage: byagent collection <name>");
  const c = await apiJson(cfg, "GET", `/api/collections/${encodeURIComponent(positional[0])}`);
  const arts = c.artifacts || [];
  return {
    json: c,
    human: [
      `${c.name}  ${c.count} artifact${c.count === 1 ? "" : "s"}  updated ${c.updated_at}`,
      ...(c.description ? [c.description] : []),
      ...(c.share_url ? [`shared ${c.share_url}`] : []),
      ...(arts.length
        ? arts.map((a, i) => `${i + 1}. ${a.id}  ${a.title || "(untitled)"}  ${a.url}${a.visibility === "private" ? "  [private]" : ""}`)
        : ["no artifacts"]),
    ],
  };
}
