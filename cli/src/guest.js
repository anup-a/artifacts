import { CliError } from "./errors.js";
import { DEFAULT_API, envApi, saveConfig, trimSlash } from "./config.js";
import { clientHeaders } from "./agent.js";

// createGuest asks the server for a 24-hour guest key and saves it as the
// config, so the next command (and the next run) uses it. A guest key
// publishes up to three public pages; claim_url moves them into a real
// workspace and retires the guest key.
export async function createGuest({ env = process.env, home } = {}) {
  const api = trimSlash(envApi(env) || DEFAULT_API);
  let res;
  try {
    res = await fetch(api + "/api/guest", { method: "POST", headers: clientHeaders() });
  } catch (err) {
    throw new CliError(`cannot reach ${api}: ${err.message}`, { code: "UNREACHABLE" });
  }
  let body = {};
  try {
    body = await res.json();
  } catch {}
  if (!res.ok || !body.token) {
    const e = body.error || {};
    throw new CliError(
      res.status === 404
        ? `${api} does not offer guest keys: run byagent login --browser to connect this agent`
        : e.message || `guest key request failed with ${res.status}`,
      { code: e.code || "NOT_CONFIGURED", exit: 2, status: res.status },
    );
  }
  const cfg = { token: body.token, api: trimSlash(body.api || api) };
  saveConfig(cfg, home ? { home } : undefined);
  return { cfg, guest: body };
}

// guestLines tells a person what a guest response means, in two lines.
export function guestLines(res) {
  if (!res || !res.guest) return [];
  return [
    `temporary guest page: it stops working at ${res.expires_at}`,
    `keep it (sign in, then confirm): ${res.claim_url}`,
  ];
}
