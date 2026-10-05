import { apiJson } from "../client.js";
import { usage } from "../errors.js";

export const RANGES = ["24h", "7d", "30d", "90d", "all"];

const LABEL = { "24h": "last 24 hours", "7d": "last 7 days", "30d": "last 30 days", "90d": "last 90 days", all: "all time" };

// Series longer than this print as a sparkline only, not a row per bucket.
const MAX_ROWS = 31;

const BLOCKS = "▁▂▃▄▅▆▇█";

// clean drops control characters, so nothing in a report can move the
// cursor or recolour the terminal. The server already reduces referrers to
// host names; titles and paths come from the publisher.
function clean(s) {
  return String(s ?? "").replace(/[\u0000-\u001f\u007f-\u009f]/g, "");
}

// table lines up columns two spaces apart: numbers, and their headers, to
// the right; text to the left.
function table(rows) {
  const numeric = rows[0].map((_, i) => rows.slice(1).every((r) => typeof r[i] === "number"));
  const widths = rows[0].map((_, i) => Math.max(...rows.map((r) => clean(r[i]).length)));
  return rows.map((r) =>
    r.map((cell, i) => {
      const s = clean(cell);
      if (numeric[i]) return s.padStart(widths[i]);
      return i === r.length - 1 ? s : s.padEnd(widths[i]);
    }).join("  ").trimEnd());
}

export function sparkline(series) {
  const max = Math.max(0, ...series.map((p) => p.views));
  if (max === 0) return "";
  return series.map((p) => (p.views === 0 ? " " : BLOCKS[Math.min(7, Math.floor((p.views / max) * 7.999))])).join("");
}

function section(title, key, rows, value) {
  if (!rows || !rows.length) return [];
  return ["", ...table([[title, "VIEWS"], ...rows.map((r) => [value(r), r[key]])])];
}

// render turns a report into the human lines both commands print.
export function render(rep, heading) {
  const t = rep.totals || {};
  const series = rep.series || [];
  const lines = [
    `${heading}, ${LABEL[rep.range] || rep.range}: ${t.range_views} views, ${t.range_visitors} visitors (${t.all_time_views} all time)`,
  ];
  const spark = sparkline(series);
  if (spark) lines.push(`${series[0].bucket}  ${spark}  ${series[series.length - 1].bucket}`);
  if (series.length && series.length <= MAX_ROWS && spark) {
    lines.push("", ...table([[rep.bucket === "hour" ? "HOUR (UTC)" : "DAY (UTC)", "VIEWS", "VISITORS"],
      ...series.map((p) => [rep.bucket === "hour" ? p.bucket.slice(0, 16).replace("T", " ") : p.bucket, p.views, p.visitors])]));
  }
  if (rep.top_pages && rep.top_pages.length) {
    lines.push("", ...table([["PAGE", "VIEWS", "VISITORS", "TITLE"],
      ...rep.top_pages.map((p) => [p.id, p.views, p.visitors, p.title || "(untitled)"])]));
  }
  lines.push(
    ...section("REFERRER", "views", rep.top_referrers, (r) => r.referrer),
    ...section("COUNTRY", "views", rep.top_countries, (r) => r.country),
    ...section("PATH", "views", rep.top_paths, (r) => (rep.top_pages ? `${r.id} ${r.path}` : r.path)),
    ...section("DEVICE", "views", rep.top_devices, (r) => r.device),
  );
  return lines;
}

function rangeOf(flags) {
  if (flags.range === undefined) return undefined;
  if (!RANGES.includes(flags.range)) throw usage(`--range takes ${RANGES.join(", ")}; got ${flags.range}`);
  return flags.range;
}

// analytics reports views of one page (with an id) or of every page in the
// workspace (without), over --range (default 30d).
export async function analytics(cfg, { positional, flags }) {
  const range = rangeOf(flags);
  if (positional.length > 1) throw usage("usage: byagent analytics [<id>] [--range 24h|7d|30d|90d|all]");
  const id = positional[0];
  const path = id ? `/api/artifacts/${encodeURIComponent(id)}/analytics` : "/api/analytics";
  const rep = await apiJson(cfg, "GET", path, { query: { range } });
  return { json: rep, human: render(rep, id ? clean(rep.id) : "all pages") };
}
