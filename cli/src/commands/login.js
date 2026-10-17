import { spawn } from "node:child_process";
import { readFileSync, rmSync, writeFileSync, mkdirSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import { apiJson } from "../client.js";
import { clientHeaders, keyName } from "../agent.js";
import { DEFAULT_API, configPath, envApi, envToken, saveConfig, trimSlash } from "../config.js";
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

// WAIT_MS bounds --wait when no --timeout is given: under the two minutes an
// agent's tool call usually gets, so a call that is still waiting comes back
// (exit 3) instead of being cut off. Running it again keeps waiting.
export const WAIT_MS = 90_000;

// handoff is what --no-wait prints: the link is for a person, so the output
// leads with the exact sentence to show them and says to stop until they act.
function handoff(p, now) {
  const left = Math.max(0, Math.round((Date.parse(p.expires_at) - now()) / 1000));
  const at = new Date(p.expires_at).toISOString().slice(11, 16);
  const message = `Open ${p.verify_url} to connect this agent to byagent (code ${p.user_code}, expires ${at} UTC).`;
  return {
    json: {
      ok: true, pending: true, status: "awaiting_user",
      verify_url: p.verify_url, user_code: p.user_code, expires_at: p.expires_at, expires_in_seconds: left,
      action_required: { for: "user", message, url: p.verify_url, code: p.user_code, expires_at: p.expires_at, expires_in_seconds: left },
      instruction: "Stop and send the user action_required.message now, before anything else. Run next only after they approve, or in the background.",
      next: "byagent login --wait --json",
    },
    human: [message, "Waiting on you: once approved, run byagent login --wait"],
  };
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
  if (noWait) return handoff(p, now);
  if (!wait) openBrowser(p.verify_url);
  note(`open ${p.verify_url} and approve code ${p.user_code} (it expires at ${p.expires_at})`);
  const expires = Date.parse(p.expires_at) || now() + 600000;
  const deadline = Math.min(expires, now() + (timeoutMs || (wait ? WAIT_MS : Infinity)));
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
      if (now() + interval > expires) {
        rmSync(pendingPath(home), { force: true });
        throw new CliError(`code ${p.user_code} expired before anyone approved it: run byagent login --browser --no-wait for a new link`, {
          code: "EXPIRED",
          exit: 3,
        });
      }
      const mins = Math.max(1, Math.round((expires - now()) / 60000));
      throw new CliError(`not approved yet: open ${p.verify_url}, approve code ${p.user_code} (about ${mins} min left), then run byagent login --wait`, {
        code: "PENDING",
        exit: 3,
      });
    }
    await sleep(interval);
  }
}

export async function login(_cfg, { flags, env = process.env, home }) {
  if (flags.guest) {
    const { cfg, guest } = await createGuest({ env: flags.api ? { ...env, BYAGENT_API: flags.api } : env });
    return {
      json: { api: cfg.api, config: configPath(), ok: true, guest: true, expires_at: guest.expires_at, claim_url: guest.claim_url },
      human: [`saved a guest key for ${cfg.api}`, ...guestLines(guest)],
    };
  }
  const name = flags.name || keyName(env);
  if (flags.code) {
    return loginWithCode(trimSlash(flags.api || envApi(env) || DEFAULT_API), flags.code, name, home);
  }
  if (flags.browser || flags.wait) {
    const timeout = flags.timeout === undefined ? 0 : Number(flags.timeout);
    if (!Number.isFinite(timeout) || timeout < 0) throw usage("--timeout must be a number of seconds");
    return browserLogin(trimSlash(flags.api || envApi(env) || DEFAULT_API), {
      name,
      wait: Boolean(flags.wait),
      noWait: Boolean(flags["no-wait"]),
      timeoutMs: timeout * 1000,
      home,
    });
  }
  const api = trimSlash(flags.api || envApi(env) || "");
  if (!api) throw usage("usage: byagent login --code <code> | --browser | --api <url> [--token <key>]  (token may come from stdin)");
  const token = flags.token || envToken(env) || (await readStdin());
  if (!token) throw usage(`no token: run byagent login --browser, or create a key at ${api}/app/keys and pass --token or pipe it on stdin`);

  // Verify before saving so a typo does not leave a config that fails later.
  await apiJson({ token, api }, "GET", "/api/artifacts", { query: { limit: 1 } });
  const path = saveConfig({ token, api });
  return { json: { api, config: path, ok: true }, human: [`saved ${path} for ${api}`] };
}
