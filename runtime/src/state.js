// Page state: window.byagent.state, a small JSON key-value store the page's
// own script keeps on byagent. Everyone who can open the page shares it, so
// a checklist, a vote or a form survives a reload and shows up for the next
// reader. It belongs to the page, not to a version, and writes never make a
// version.
//
// Reads load the whole state once, then answer from memory. While the page
// is open and has used its state, it asks every POLL_MS (and whenever the
// tab comes back) whether anyone else wrote, and fires "change" when they
// did. Nothing here runs until the page first touches window.byagent.state.

const KEY = /^[A-Za-z0-9._:-]{1,128}$/;
const VALUE_MAX = 64 * 1024;
const PATCH_MAX = 256;
const POLL_MS = 15000;
const TIMEOUT_MS = 10000;

function clone(v) {
  return v === undefined ? undefined : JSON.parse(JSON.stringify(v));
}

function bytes(s) {
  return new TextEncoder().encode(s).length;
}

class StateError extends Error {
  constructor(message, status, code) {
    super(message);
    this.name = "StateError";
    this.status = status;
    this.code = code;
  }
}

// toPatch checks a {key: value | null} map and returns it with each value
// as the JSON it will be stored as (null for a delete).
function toPatch(obj) {
  if (!obj || typeof obj !== "object" || Array.isArray(obj)) throw new TypeError("byagent.state.patch takes an object of {key: value or null}");
  const keys = Object.keys(obj);
  if (keys.length === 0) throw new TypeError("byagent.state: nothing to write");
  if (keys.length > PATCH_MAX) throw new RangeError(`byagent.state: at most ${PATCH_MAX} keys per write`);
  const out = {};
  for (const k of keys) {
    if (!KEY.test(k)) throw new TypeError(`byagent.state: bad key ${JSON.stringify(k)}; use 1-128 of A-Z a-z 0-9 . _ : -`);
    const v = obj[k];
    if (v === null || v === undefined) {
      out[k] = null;
      continue;
    }
    const text = JSON.stringify(v);
    if (text === undefined) throw new TypeError(`byagent.state: the value for ${k} is not JSON`);
    if (bytes(text) > VALUE_MAX) throw new RangeError(`byagent.state: the value for ${k} is over 64 KiB`);
    out[k] = JSON.parse(text);
  }
  return out;
}

export function createState({ url, fetchImpl = (...a) => fetch(...a), win = window, doc = document, pollMs = POLL_MS }) {
  let items = null; // null until the first load
  let revision = 0;
  let loading = null;
  let queue = Promise.resolve();
  let timer = null;
  let off = false; // the page's state is switched off or the page is gone
  const handlers = new Set();

  async function call(method, query = "", body) {
    const ctrl = new AbortController();
    const t = setTimeout(() => ctrl.abort(), TIMEOUT_MS);
    try {
      const init = { method, signal: ctrl.signal, credentials: "same-origin", cache: "no-store" };
      if (body !== undefined) {
        init.headers = { "Content-Type": "application/json" };
        init.body = JSON.stringify(body);
      }
      const res = await fetchImpl(url + query, init);
      const data = await res.json().catch(() => null);
      if (!res.ok) {
        if (res.status === 404 || res.status === 410) off = true;
        const err = (data && data.error) || {};
        throw new StateError(err.message || `HTTP ${res.status}`, res.status, err.code);
      }
      return data || {};
    } catch (e) {
      if (e instanceof StateError) throw e;
      throw new StateError(e && e.name === "AbortError" ? "timed out" : "network error", 0);
    } finally {
      clearTimeout(t);
    }
  }

  function emit(detail) {
    for (const fn of [...handlers]) {
      try {
        fn(detail);
      } catch {
        /* one listener's bug must not stop the others */
      }
    }
    try {
      win.dispatchEvent(new CustomEvent("byagent:state", { detail }));
    } catch {
      /* no CustomEvent: listeners via on() still ran */
    }
  }

  function startPolling() {
    if (timer || off || !pollMs) return;
    timer = setInterval(() => {
      if (doc.visibilityState !== "hidden") refresh(false).catch(() => {});
    }, pollMs);
    const wake = () => {
      if (doc.visibilityState !== "hidden") refresh(false).catch(() => {});
    };
    win.addEventListener("focus", wake);
    doc.addEventListener("visibilitychange", wake);
  }

  function load() {
    if (!loading) {
      loading = call("GET").then(
        (d) => {
          items = d.items || {};
          revision = d.revision || 0;
          startPolling();
          emit({ source: "initial", revision, items: clone(items) });
        },
        (e) => {
          loading = null;
          throw e;
        }
      );
    }
    return loading;
  }

  // refresh asks whether anyone wrote since our revision and, if so, takes
  // the new state and reports the keys that changed. full skips the
  // question, for when what we hold may be wrong at our own revision.
  async function refresh(full) {
    if (items === null) return load();
    if (off) return;
    const d = await call("GET", full ? "" : `?since=${revision}`);
    if (!d.items || (!full && d.revision === revision)) return;
    const next = d.items;
    const changes = {};
    for (const k of new Set([...Object.keys(items), ...Object.keys(next)])) {
      const was = k in items ? JSON.stringify(items[k]) : undefined;
      const now = k in next ? JSON.stringify(next[k]) : undefined;
      if (was !== now) changes[k] = k in next ? clone(next[k]) : null;
    }
    items = next;
    revision = d.revision;
    if (Object.keys(changes).length) emit({ source: "remote", revision, changes });
  }

  function write(obj) {
    let patch;
    try {
      patch = toPatch(obj);
    } catch (e) {
      return Promise.reject(e);
    }
    // One write at a time, in the order the page made them.
    const run = queue.then(async () => {
      await load();
      const before = revision;
      const d = await call("POST", "", { items: patch });
      for (const [k, v] of Object.entries(patch)) {
        if (v === null) delete items[k];
        else items[k] = v;
      }
      revision = d.revision;
      emit({ source: "local", revision, changes: clone(patch) });
      // Someone else wrote in between: what we hold is our write over an
      // old copy, so fetch the real thing.
      if (d.revision !== before + 1) refresh(true).catch(() => {});
      return revision;
    });
    queue = run.catch(() => {});
    return run;
  }

  const api = {
    async get(key) {
      await load();
      return key in items ? clone(items[key]) : null;
    },
    async all() {
      await load();
      return clone(items);
    },
    set(key, value) {
      return write({ [key]: value });
    },
    delete(key) {
      return write({ [key]: null });
    },
    patch(obj) {
      return write(obj);
    },
    refresh() {
      return refresh(false);
    },
    on(event, fn) {
      if (event !== "change" || typeof fn !== "function") throw new TypeError('byagent.state.on takes ("change", function)');
      handlers.add(fn);
      if (items !== null) {
        const snapshot = { source: "initial", revision, items: clone(items) };
        setTimeout(() => handlers.has(fn) && fn(snapshot), 0);
      } else {
        load().catch(() => {});
      }
      return () => handlers.delete(fn);
    },
    off(event, fn) {
      handlers.delete(fn);
    },
    get revision() {
      return revision;
    },
  };
  return Object.freeze(api);
}

// installState puts the store on window.byagent.state for the page at base
// ("/" on its own host, the mount on a custom domain, /a/{id}/ otherwise).
export function installState(cfg) {
  const base = cfg.base || `/a/${encodeURIComponent(cfg.artifactId)}/`;
  const state = createState({ url: `${base}_state` });
  const ns = window.byagent && typeof window.byagent === "object" ? window.byagent : {};
  ns.state = state;
  window.byagent = ns;
  return state;
}
