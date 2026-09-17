// Thin wrappers over the /c/* viewer API. Every call is fail-soft: the host page
// must render correctly even when the comments backend is unreachable.

const TIMEOUT_MS = 8000;

async function request(url, options) {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), TIMEOUT_MS);
  try {
    // same-origin, not omit: an artifact shared by code is read by someone
    // holding an unlock cookie, and omitting it makes every viewer call 404
    // for exactly the people the code was sent to. The cookie is HttpOnly,
    // scoped to this artifact's own paths, and lives on the share origin,
    // which never holds a dashboard session, so nothing ambient rides along.
    const res = await fetch(url, { ...options, signal: ctrl.signal, credentials: "same-origin" });
    const text = await res.text();
    let body = null;
    try {
      body = text ? JSON.parse(text) : null;
    } catch {
      body = null;
    }
    if (!res.ok) {
      const message = (body && body.error && body.error.message) || `HTTP ${res.status}`;
      return { ok: false, error: message, status: res.status };
    }
    return { ok: true, data: body };
  } catch (err) {
    return { ok: false, error: err && err.name === "AbortError" ? "timed out" : "network error", status: 0 };
  } finally {
    clearTimeout(timer);
  }
}

function json(body) {
  return { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) };
}

export function createClient({ artifactId, version, origin = "" }) {
  const base = `${origin}/c/${encodeURIComponent(artifactId)}`;
  return {
    meta: () => request(`${base}/meta`, { method: "GET" }),
    listThreads: (status = "open") =>
      request(`${base}/comments?status=${encodeURIComponent(status)}`, { method: "GET" }),
    createThread: (author_name, body, anchor, idempotencyKey) => {
      const opts = json({ author_name, body, anchor, version });
      if (idempotencyKey) opts.headers["Idempotency-Key"] = idempotencyKey;
      return request(`${base}/comments`, opts);
    },
    reply: (threadId, author_name, body) =>
      request(`${base}/comments/${encodeURIComponent(threadId)}/reply`, json({ author_name, body })),
    seen: (threadId, anchor_status) =>
      request(`${base}/comments/${encodeURIComponent(threadId)}/seen`, json({ version, anchor_status })),
  };
}
