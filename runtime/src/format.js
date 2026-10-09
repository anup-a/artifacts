// Small presentational helpers shared by the panel rows.

import { LANG } from "./i18n.js";

/**
 * English stays compact: "now", "20m", "2h", "3d". Other languages get the
 * browser's short relative format ("20分钟前", "vor 20 Min."). Past a week,
 * a short date in the reader's language ("Jul 1", "7月1日"), with the year
 * instead of the day once it is not this year.
 */
export function relativeTime(iso, now = Date.now(), lang = LANG) {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "";
  const s = Math.max(0, Math.round((now - t) / 1000));
  if (s < 7 * 86400) {
    const [n, unit] =
      s < 60 ? [0, "second"] : s < 3600 ? [Math.floor(s / 60), "minute"] : s < 86400 ? [Math.floor(s / 3600), "hour"] : [Math.floor(s / 86400), "day"];
    if (lang !== "en") {
      try {
        return new Intl.RelativeTimeFormat(lang, { style: "short", numeric: "auto" }).format(-n, unit);
      } catch {
        /* no Intl.RelativeTimeFormat: fall through to the compact English */
      }
    }
    return n ? `${n}${unit[0]}` : "now";
  }
  const d = new Date(t);
  const sameYear = d.getUTCFullYear() === new Date(now).getUTCFullYear();
  try {
    return d.toLocaleDateString(lang, { month: "short", timeZone: "UTC", ...(sameYear ? { day: "numeric" } : { year: "numeric" }) });
  } catch {
    return d.toISOString().slice(0, 10);
  }
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
