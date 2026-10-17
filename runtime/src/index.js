// Boot sequence for the injected comments runtime.
//
// Hard rule: nothing here may break the host page. Every step is wrapped, and a
// failure leaves the page exactly as the publisher wrote it.
import { createClient } from "./api.js";
import { mountPanel } from "./panel.js";
import { mountCompose } from "./compose.js";
import { resolveAnchor } from "./dom.js";
import { paintThreads, clearHighlights } from "./highlight.js";
import { LANG } from "./i18n.js";
import { installState } from "./state.js";

const HOST_ID = "artifacts-rt";

function readConfig() {
  const el =
    document.currentScript ||
    document.querySelector('script[src*="/_rt/comments.js"][data-artifact]');
  if (!el) return null;
  const artifactId = el.getAttribute("data-artifact");
  const version = parseInt(el.getAttribute("data-version") || "0", 10);
  if (!artifactId || !Number.isFinite(version) || version <= 0) return null;
  // data-base is where the page lives on a host of its own ("/") or a custom
  // domain ("/docs/"); on byagent.dev it is absent and the page lives at
  // /a/<id>/.
  const base = el.getAttribute("data-base") || "";
  // data-comments="off": the page's comments are off and the runtime is
  // here for page state alone, with no panel.
  const comments = el.getAttribute("data-comments") !== "off";
  return { artifactId, version, comments, base: base.startsWith("/") && base.endsWith("/") ? base : "" };
}

function storage(artifactId) {
  const key = (name) => `artifacts:${artifactId}:${name}`;
  return {
    get(name) {
      try {
        return localStorage.getItem(key(name));
      } catch {
        return null;
      }
    },
    set(name, value) {
      try {
        localStorage.setItem(key(name), value);
      } catch {
        /* private mode: remembering the name is a nicety, not a requirement */
      }
    },
  };
}

// The script tag is read at parse time; document.currentScript is null later.
const CONFIG = readConfig();

// Page state goes on window.byagent right away, before DOMContentLoaded, so
// the page's own handlers for it can use it.
if (CONFIG) {
  try {
    installState(CONFIG);
  } catch {
    /* the page keeps working without it */
  }
}

async function boot() {
  const cfg = CONFIG;
  if (!cfg || !cfg.comments) return;

  const host = document.createElement("div");
  host.id = HOST_ID;
  // The reader's language, not the page's: picks the right CJK glyphs and
  // hyphenation for the panel's own text.
  host.lang = LANG;
  const shadow = host.attachShadow({ mode: "open" });
  document.body.appendChild(host);

  const client = createClient({ artifactId: cfg.artifactId, version: cfg.version, origin: "" });
  const store = storage(cfg.artifactId);

  const state = {
    threads: [],
    resolved: new Map(), // threadId -> {status, range, element}
    versions: [],
    currentVersion: cfg.version,
    unavailable: false,
  };

  const panel = mountPanel(shadow, { state, client, store, config: cfg, onRefresh: refresh, onFilter: paint });

  // Highlights follow the panel's filter: resolved threads only light up the
  // page while the viewer is looking at them.
  //
  // Anchors are resolved here, after the previous paint's marks are stripped:
  // a Range measured while a <mark> is still wrapped around the text collapses
  // to nothing the moment that mark is unwrapped and its text nodes merged.
  function paint(threads) {
    clearHighlights();
    state.resolved = new Map();
    for (const t of state.threads) {
      state.resolved.set(t.id, resolveAnchor(t.anchor));
    }
    paintThreads({ threads, resolved: state.resolved }, (id) => panel.focusThread(id));
  }

  async function refresh() {
    const [list, meta] = await Promise.all([client.listThreads("all"), client.meta()]);
    if (meta.ok && meta.data) {
      state.versions = Array.isArray(meta.data.versions) ? meta.data.versions : [];
      state.currentVersion = meta.data.current_version || cfg.version;
    }
    if (!list.ok) {
      state.unavailable = true;
      state.threads = [];
      panel.render();
      return;
    }
    state.unavailable = false;
    state.threads = (list.data && list.data.threads) || [];
    paint(panel.visibleThreads());
    panel.render();
    reportSeen();
  }

  // Tell the owner where each thread actually landed, so `artifacts comments`
  // shows live anchor health. Only meaningful on the current version; the server
  // ignores the beacon otherwise, and we skip it to save the request.
  function reportSeen() {
    if (cfg.version !== state.currentVersion) return;
    for (const t of state.threads) {
      const r = state.resolved.get(t.id);
      if (r && r.status !== t.anchor_status) client.seen(t.id, r.status);
    }
  }

  mountCompose(shadow, { state, client, store, config: cfg, onCreated: refresh, panel });

  await refresh();

  // Test hook: never relied upon by the UI itself.
  window.__artifactsRuntime = { state, refresh, client };
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", () => {
    boot().catch(() => {});
  });
} else {
  boot().catch(() => {});
}
