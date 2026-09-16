// Small presentational helpers shared by the panel rows.

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

/** "now", "20m", "2h", "3d", then a short date once it is more than a week old. */
export function relativeTime(iso, now = Date.now()) {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "";
  const s = Math.max(0, Math.round((now - t) / 1000));
  if (s < 60) return "now";
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  if (s < 7 * 86400) return `${Math.floor(s / 86400)}d`;
  const d = new Date(t);
  const n = new Date(now);
  if (d.getUTCFullYear() === n.getUTCFullYear()) return `${MONTHS[d.getUTCMonth()]} ${d.getUTCDate()}`;
  return `${MONTHS[d.getUTCMonth()]} ${d.getUTCFullYear()}`;
}

/** Index into the six-colour avatar palette, derived from the author name. */
export function avatarHue(name) {
  // FNV-1a: cheap and spreads short names better than the classic 31 multiply.
  let h = 2166136261;
  for (const ch of String(name || "")) {
    h ^= ch.charCodeAt(0);
    h = Math.imul(h, 16777619) >>> 0;
  }
  return (h >>> 8) % 6;
}

export function initial(name) {
  const s = String(name || "").trim();
  return s ? s[0].toUpperCase() : "?";
}
