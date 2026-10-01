import { apiJson } from "../client.js";
import { configPath, saveConfig, trimSlash } from "../config.js";
import { usage } from "../errors.js";
import { createGuest, guestLines } from "../guest.js";

// readStdin lets `echo $KEY | artifacts login --api ...` work without the key
// ever appearing in argv, where any process on the box could read it.
async function readStdin() {
  if (process.stdin.isTTY) return "";
  const chunks = [];
  for await (const chunk of process.stdin) chunks.push(chunk);
  return Buffer.concat(chunks).toString("utf8").trim();
}

export async function login(_cfg, { flags, env = process.env }) {
  if (flags.guest) {
    const { cfg, guest } = await createGuest({ env: flags.api ? { ...env, ARTIFACTS_API: flags.api } : env });
    return {
      json: { api: cfg.api, config: configPath(), ok: true, guest: true, expires_at: guest.expires_at, claim_url: guest.claim_url },
      human: [`saved a guest key for ${cfg.api}`, ...guestLines(guest)],
    };
  }
  const api = trimSlash(flags.api || env.ARTIFACTS_API || "");
  if (!api) throw usage("usage: artifacts login --api <url> [--token <key>]  (token may come from stdin)");
  const token = flags.token || env.ARTIFACTS_TOKEN || (await readStdin());
  if (!token) throw usage(`no token: create a key at ${api}/app/keys, then pass --token or pipe it on stdin`);

  // Verify before saving so a typo does not leave a config that fails later.
  await apiJson({ token, api }, "GET", "/api/artifacts", { query: { limit: 1 } });
  const path = saveConfig({ token, api });
  return { json: { api, config: path, ok: true }, human: [`saved ${path} for ${api}`] };
}
