import { CliError } from "./errors.js";

function url(cfg, path, query) {
  const u = new URL(cfg.api + path);
  for (const [k, v] of Object.entries(query || {})) {
    if (v !== undefined && v !== null && v !== "") u.searchParams.set(k, String(v));
  }
  return u;
}

async function send(cfg, method, path, { query, body, headers } = {}) {
  let res;
  try {
    res = await fetch(url(cfg, path, query), {
      method,
      headers: { Authorization: `Bearer ${cfg.token}`, ...headers },
      body,
    });
  } catch (err) {
    throw new CliError(`cannot reach ${cfg.api}: ${err.message}`, { code: "UNREACHABLE" });
  }
  return res;
}

// apiJson returns the parsed body, or null for 204. Any non-2xx becomes a
// CliError carrying the server's error code and request id.
export async function apiJson(cfg, method, path, opts = {}) {
  const res = await send(cfg, method, path, opts);
  const requestId = res.headers.get("X-Request-Id") || "";
  if (res.status === 204) return null;
  const text = await res.text();
  let parsed = null;
  try {
    parsed = text ? JSON.parse(text) : null;
  } catch {
    parsed = null;
  }
  if (!res.ok) {
    const e = parsed && parsed.error ? parsed.error : {};
    throw new CliError(e.message || `request failed with ${res.status}`, {
      code: e.code || "HTTP_ERROR",
      status: res.status,
      requestId,
    });
  }
  return parsed;
}
