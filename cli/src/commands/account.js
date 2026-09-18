import { apiJson } from "../client.js";
import { usage } from "../errors.js";

export async function whoami(cfg) {
  const identity = await apiJson(cfg, "GET", "/api/me");
  const { tenant, key } = identity;
  return {
    json: identity,
    human: [
      `${tenant.name} (${tenant.kind}, ${tenant.id})`,
      `key ${key.name || "(unnamed)"} (${key.id})`,
    ],
  };
}

export async function portalLink(cfg, { positional }) {
  if (!positional[0]) throw usage("usage: byagent portal-link <end-user id>");
  const result = await apiJson(cfg, "POST", `/api/end-users/${encodeURIComponent(positional[0])}/portal-link`);
  return { json: result, human: [result.url] };
}
