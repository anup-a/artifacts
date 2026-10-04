import { spawn } from "node:child_process";
import { readFileSync, rmSync, writeFileSync, mkdirSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import { apiJson } from "../client.js";
import { clientHeaders, keyName } from "../agent.js";
import { DEFAULT_API, configPath, saveConfig, trimSlash } from "../config.js";
import { CliError, usage } from "../errors.js";
import { createGuest, guestLines } from "../guest.js";

// readStdin lets `echo $KEY | byagent login --api ...` work without the key
// ever appearing in argv, where any process on the box could read it.
async function readStdin() {
  if (process.stdin.isTTY) return "";
  const chunks = [];
  for await (const chunk of process.stdin) chunks.push(chunk);
  return Buffer.concat(chunks).toString("utf8").trim();
}

// post sends JSON to an unauthenticated connect route and returns the status
// with the parsed body, leaving it to the caller which statuses are errors.
async function post(api, path, body) {
  let res;
  try {
    res = await fetch(api + path, {
      method: "POST",
      headers: { ...clientHeaders(), "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
  } catch (err) {
    throw new CliError(`cannot reach ${api}: ${err.message}`, { code: "UNREACHABLE" });
  }
  let parsed = {};
  try {
    parsed = await res.json();
  } catch {}
  return { status: res.status, body: parsed || {}, requestId: res.headers.get("X-Request-Id") || "" };
}

function failure({ status, body, requestId }, fallback) {
  const e = body.error || {};
  return new CliError(e.message || `${fallback} failed with ${status}`, { code: e.code || "HTTP_ERROR", status, requestId });
}

// connected saves the key the server issued and says where it went. The key
// itself is never printed.
function connected(api, body, home) {
  const path = saveConfig({ token: body.token, api: trimSlash(body.api || api) }, home ? { home } : undefined);
  const ws = body.workspace || {};
  const key = body.key || {};
  return {
    json: { api: trimSlash(body.api || api), config: path, ok: true, workspace: ws, key: { name: key.name, prefix: key.prefix } },
    human: [`connected to ${ws.name || "your workspace"} as "${key.name}"`, `saved ${path}`],
  };
}

function pendingPath(home = homedir()) {
  return join(home, ".artifacts", "pending-login.json");
}

function savePending(p, home = homedir()) {
  mkdirSync(join(home, ".artifacts"), { recursive: true, mode: 0o700 });
  writeFileSync(pendingPath(home), JSON.stringify(p, null, 2) + "\n", { mode: 0o600 });
}

function readPending(home = homedir()) {
  try {
    return JSON.parse(readFileSync(pendingPath(home), "utf8"));
  } catch {
    throw usage("nothing to wait for: run byagent login --browser first");
  }
}

// openBrowser is a convenience for a person at a terminal. Agents have no
// screen, so it only runs when stdout is a terminal.
function openBrowser(url) {
  if (!process.stdout.isTTY) return;
  const cmd = process.platform === "darwin" ? "open" : process.platform === "win32" ? "explorer" : "xdg-open";
  try {
    spawn(cmd, [url], { stdio: "ignore", detached: true }).on("error", () => {}).unref();
  } catch {}
}

// loginWithCode trades a one-time code from the dashboard's Connect agent
// page for a key named after this agent.
async function loginWithCode(api, code, name, home) {
  const r = await post(api, "/api/connect", { code, name });
  if (r.status !== 201 || !r.body.token) throw failure(r, "connect");
  return connected(api, r.body, home);
}

// browserLogin asks the server for an approval link, then waits until a
// signed-in person approves it. --no-wait stops after printing the link and
// --wait picks the same request up again, so an agent never has to block on
// a person.
export async function browserLogin(api, { name, wait, noWait, timeoutMs, home, note, sleep, now = Date.now } = {}) {
  sleep ||= (ms) => new Promise((r) => setTimeout(r, ms));
  note ||= (line) => process.stderr.write(line + "\n");
  let p;
  if (wait) {
    p = readPending(home);
  } else {
    const r = await post(api, "/api/connect/device", { name });
    if (r.status !== 201) throw failure(r, "connect");
    p = { api, ...r.body };
    savePending(p, home);
  }
  const lines = [`open ${p.verify_url}`, `and approve code ${p.user_code} (it expires at ${p.expires_at})`];
  if (noWait) {
    return {
      json: { ok: false, pending: true, verify_url: p.verify_url, user_code: p.user_code, expires_at: p.expires_at, next: "byagent login --wait" },
      human: [...lines, "then run: byagent login --wait"],
    };
  }
  if (!wait) openBrowser(p.verify_url);
  note(lines.join(" "));
  const deadline = Math.min(Date.parse(p.expires_at) || now() + 600000, timeoutMs ? now() + timeoutMs : Infinity);
  const interval = Math.max(1, Number(p.interval) || 2) * 1000;
  for (;;) {
    const r = await post(p.api, "/api/connect/device/token", { device_code: p.device_code });
    if (r.status === 201 && r.body.token) {
      rmSync(pendingPath(home), { force: true });
      return connected(p.api, r.body, home);
    }
    if (r.status !== 202) {
      rmSync(pendingPath(home), { force: true });
      throw failure(r, "connect");
    }
    if (now() + interval > deadline) {
      throw new CliError(`not approved yet: open ${p.verify_url}, approve code ${p.user_code}, then run byagent login --wait`, {
        code: "PENDING",
        exit: 3,
      });
    }
    await sleep(interval);
  }
}

export async function login(_cfg, { flags, env = process.env, home }) {
  if (flags.guest) {
    const { cfg, guest } = await createGuest({ env: flags.api ? { ...env, ARTIFACTS_API: flags.api } : env });
    return {
      json: { api: cfg.api, config: configPath(), ok: true, guest: true, expires_at: guest.expires_at, claim_url: guest.claim_url },
      human: [`saved a guest key for ${cfg.api}`, ...guestLines(guest)],
    };
  }
  const name = flags.name || keyName(env);
  if (flags.code) {
    return loginWithCode(trimSlash(flags.api || env.ARTIFACTS_API || DEFAULT_API), flags.code, name, home);
  }
  if (flags.browser || flags.wait) {
    const timeout = flags.timeout === undefined ? 0 : Number(flags.timeout);
    if (!Number.isFinite(timeout) || timeout < 0) throw usage("--timeout must be a number of seconds");
    return browserLogin(trimSlash(flags.api || env.ARTIFACTS_API || DEFAULT_API), {
      name,
      wait: Boolean(flags.wait),
      noWait: Boolean(flags["no-wait"]),
      timeoutMs: timeout * 1000,
      home,
    });
  }
  const api = trimSlash(flags.api || env.ARTIFACTS_API || "");
  if (!api) throw usage("usage: byagent login --code <code> | --browser | --api <url> [--token <key>]  (token may come from stdin)");
  const token = flags.token || env.ARTIFACTS_TOKEN || (await readStdin());
  if (!token) throw usage(`no token: run byagent login --browser, or create a key at ${api}/app/keys and pass --token or pipe it on stdin`);

  // Verify before saving so a typo does not leave a config that fails later.
  await apiJson({ token, api }, "GET", "/api/artifacts", { query: { limit: 1 } });
  const path = saveConfig({ token, api });
  return { json: { api, config: path, ok: true }, human: [`saved ${path} for ${api}`] };
}
