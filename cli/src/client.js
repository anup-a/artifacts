import { CliError } from "./errors.js";
import { clientHeaders } from "./agent.js";

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
      headers: { ...clientHeaders(), Authorization: `Bearer ${cfg.token}`, ...headers },
      body,
    });
  } catch (err) {
    throw new CliError(`cannot reach ${cfg.api}: ${err.message}`, { code: "UNREACHABLE" });
  }
  return res;
}

// rawError turns a failed raw read into a CliError carrying the server's code
// like apiJson's do.
async function rawError(res) {
  let e = {};
  try {
    e = JSON.parse(await res.text()).error || {};
  } catch {}
  return new CliError(e.message || `request failed with ${res.status}`, {
    code: e.code || "HTTP_ERROR",
    status: res.status,
    requestId: res.headers.get("X-Request-Id") || "",
  });
}

// apiText returns a raw body, such as a published file read back.
export async function apiText(cfg, method, path, opts = {}) {
  const res = await send(cfg, method, path, opts);
  if (!res.ok) throw await rawError(res);
  return res.text();
}

// apiBytes is apiText for files that may not be text, such as images.
export async function apiBytes(cfg, method, path, opts = {}) {
  const res = await send(cfg, method, path, opts);
  if (!res.ok) throw await rawError(res);
  return Buffer.from(await res.arrayBuffer());
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
    // A key that stopped working (revoked, or a guest key whose pages were
    // claimed) is fixed the same way every time: connect again.
    const hint = res.status === 401 && cfg.token ? "; connect again with byagent login --browser" : "";
    throw new CliError((e.message || `request failed with ${res.status}`) + hint, {
      code: e.code || "HTTP_ERROR",
      status: res.status,
      requestId,
    });
  }
  return parsed;
}
