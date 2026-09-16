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

// Env wins over the file so CI can override a developer's saved token.
export function resolveConfig({ env = process.env, home = homedir() } = {}) {
  const file = env.ARTIFACTS_TOKEN && env.ARTIFACTS_API ? {} : readFile(home);
  const token = env.ARTIFACTS_TOKEN || file.token || "";
  const api = trimSlash(env.ARTIFACTS_API || file.api || "");
  if (!token || !api) {
    throw new CliError(
      "not configured: set ARTIFACTS_TOKEN and ARTIFACTS_API, or run artifacts login",
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

export function trimSlash(url) {
  return url.replace(/\/+$/, "");
}
