import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import { CliError } from "./errors.js";

export function configPath(home = homedir()) {
  return join(home, ".artifacts", "config.json");
}

function readFile(home) {
  try {
    const raw = readFileSync(configPath(home), "utf8");
    const parsed = JSON.parse(raw);
    return parsed && typeof parsed === "object" ? parsed : {};
  } catch (err) {
    if (err.code === "ENOENT") return {};
    // A corrupt config is worth saying out loud: silently ignoring it would make
    // "not configured" the error for a file the user believes they wrote.
    throw new CliError(`cannot read ${configPath(home)}: ${err.message}`, { exit: 2, code: "CONFIG" });
  }
}

// envToken and envApi read BYAGENT_TOKEN / BYAGENT_API, or the older
// ARTIFACTS_TOKEN / ARTIFACTS_API names, which keep working.
export const envToken = (env) => env.BYAGENT_TOKEN || env.ARTIFACTS_TOKEN || "";
export const envApi = (env) => env.BYAGENT_API || env.ARTIFACTS_API || "";

// Env wins over the file so CI can override a developer's saved token.
export function resolveConfig({ env = process.env, home = homedir() } = {}) {
  const file = envToken(env) && envApi(env) ? {} : readFile(home);
  const token = envToken(env) || file.token || "";
  const api = trimSlash(envApi(env) || file.api || "");
  if (!token || !api) {
    // Never "artifacts login": npx artifacts is someone else's package.
    throw new CliError(
      "not configured: run byagent login --browser --no-wait --json to connect, or set BYAGENT_TOKEN and BYAGENT_API",
      { exit: 2, code: "NOT_CONFIGURED" },
    );
  }
  return { token, api };
}

export function saveConfig({ token, api }, { home = homedir() } = {}) {
  const path = configPath(home);
  mkdirSync(join(home, ".artifacts"), { recursive: true, mode: 0o700 });
  writeFileSync(path, JSON.stringify({ token, api: trimSlash(api) }, null, 2) + "\n", { mode: 0o600 });
  return path;
}

// DEFAULT_API is where a guest key comes from when nothing is configured.
export const DEFAULT_API = "https://app.byagent.dev";

export function trimSlash(url) {
  return url.replace(/\/+$/, "");
}
