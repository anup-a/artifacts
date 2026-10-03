import { apiJson } from "../client.js";
import { usage } from "../errors.js";

function quoteOf(anchor) {
  if (!anchor || typeof anchor !== "object") return "";
  return anchor.kind === "element" ? anchor.text || anchor.css || "" : anchor.quote || "";
}

function threadLines(t) {
  const out = [`${t.id}  ${t.status}${t.progress ? `  ${t.progress}` : ""}  anchor ${t.anchor_status}  v${t.version}`];
  const q = quoteOf(t.anchor);
  if (q) out.push(`  on: ${q}`);
  if (t.suggestion) out.push(`  suggests: ${t.suggestion}`);
  for (const c of t.comments || []) out.push(`  ${c.author_name} (${c.author_kind}): ${c.body}`);
  return out;
}

const WAIT_USAGE = "usage: byagent comments <id> --wait [--timeout <seconds>] [--interval <seconds>] [--since <rfc3339>]";

function seconds(flags, name, fallback, min) {
  if (flags[name] === undefined) return fallback;
  const n = Number(flags[name]);
  if (!Number.isFinite(n) || n < min) throw usage(`--${name} must be a number of seconds, at least ${min}`);
  return n;
}

// A poll that fails because the network or the server hiccuped is retried on
// the next tick; anything else (a bad key, a deleted artifact) ends the wait.
function transient(err) {
  return err.code === "UNREACHABLE" || err.status === 429 || err.status >= 500;
}

// waitForComments blocks until a reader comment appears that was not there
// when the wait started (or, with since, that was written after since), then
// returns the threads holding new reader comments. Owner replies, including
// the agent's own, never wake it. It works on comment ids rather than the
// server's since filter, which only matches when a thread was created, so a
// reader replying inside an existing thread still counts.
export async function waitForComments(cfg, id, { intervalMs, timeoutMs, since, sleep = (ms) => new Promise((r) => setTimeout(r, ms)), now = Date.now } = {}) {
  const path = `/api/artifacts/${encodeURIComponent(id)}/comments`;
  const sinceMs = since ? Date.parse(since) : NaN;
  const started = now();
  let seen = null;
  for (;;) {
    let threads;
    try {
      threads = (await apiJson(cfg, "GET", path, { query: { status: "all" } })).threads || [];
    } catch (err) {
      if (!transient(err)) throw err;
      threads = null;
    }
    if (threads) {
      const readerComments = (t) => (t.comments || []).filter((c) => c.author_kind !== "owner");
      if (seen === null) {
        seen = new Set();
        for (const t of threads) {
          for (const c of readerComments(t)) {
            if (Number.isNaN(sinceMs) || Date.parse(c.created_at) <= sinceMs) seen.add(c.id);
          }
        }
      }
      const fresh = threads.filter((t) => readerComments(t).some((c) => !seen.has(c.id)));
      if (fresh.length) return { threads: fresh, timed_out: false };
    }
    if (now() - started + intervalMs > timeoutMs) return { threads: [], timed_out: true };
    await sleep(intervalMs);
  }
}

export async function comments(cfg, { positional, flags }) {
  if (!positional[0]) throw usage("usage: byagent comments <id> [--open|--all] [--since <rfc3339>] [--wait]");
  if (flags.open && flags.all) throw usage("--open and --all are mutually exclusive");
  if (flags.since && Number.isNaN(Date.parse(flags.since))) throw usage("--since must be an RFC 3339 time");
  if (flags.wait) {
    if (flags.open || flags.all) throw usage(WAIT_USAGE);
    const timeout = seconds(flags, "timeout", 1800, 1);
    const interval = seconds(flags, "interval", 20, 5);
    const res = await waitForComments(cfg, positional[0], { intervalMs: interval * 1000, timeoutMs: timeout * 1000, since: flags.since });
    return {
      json: res,
      human: res.timed_out ? [`no new comments after ${timeout}s`] : res.threads.flatMap(threadLines),
    };
  }
  const status = flags.all ? "all" : "open";
  const res = await apiJson(cfg, "GET", `/api/artifacts/${encodeURIComponent(positional[0])}/comments`, {
    query: { status, since: flags.since },
  });
  const threads = res.threads || [];
  return {
    json: res,
    human: threads.length ? threads.flatMap(threadLines) : [`no ${status === "all" ? "" : "open "}comments`],
  };
}

export async function reply(cfg, { positional }) {
  const [id, thread, body] = positional;
  if (!id || !thread || !body) throw usage('usage: byagent reply <id> <thread> "<text>"');
  const c = await apiJson(cfg, "POST", `/api/artifacts/${encodeURIComponent(id)}/comments/${encodeURIComponent(thread)}/reply`, {
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ body }),
  });
  return { json: c, human: [`replied to ${thread}`] };
}

export async function resolve(cfg, { positional }) {
  const [id, thread] = positional;
  if (!id || !thread) throw usage("usage: byagent resolve <id> <thread>");
  const t = await apiJson(cfg, "POST", `/api/artifacts/${encodeURIComponent(id)}/comments/${encodeURIComponent(thread)}/resolve`, {});
  return { json: t, human: [`resolved ${thread}`] };
}

const PROGRESS = ["seen", "working", "declined"];

// mark tells readers what is happening to their comment: seen, working, or
// declined (which needs --note with the reason and also resolves the thread).
export async function mark(cfg, { positional, flags }) {
  const [id, thread, progress] = positional;
  if (!id || !thread || !PROGRESS.includes(progress)) {
    throw usage('usage: byagent mark <id> <thread> seen|working|declined [--note "<text>"]');
  }
  if (progress === "declined" && !flags.note) throw usage("declined needs --note saying why");
  const body = { progress };
  if (flags.note) body.note = String(flags.note);
  const t = await apiJson(cfg, "POST", `/api/artifacts/${encodeURIComponent(id)}/comments/${encodeURIComponent(thread)}/progress`, {
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  return { json: t, human: [`${thread}: ${progress}${t.status === "resolved" ? ", resolved" : ""}`] };
}
