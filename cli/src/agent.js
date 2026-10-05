import { readFileSync } from "node:fs";
import { hostname } from "node:os";

// detectAgent names the agent running this command, from what it sets in the
// environment of the commands it runs. BYAGENT_AGENT wins. Only markers seen
// in the real tools count:
//   Codex sets CODEX_THREAD_ID, CODEX_SESSION_ID and, when sandboxed, CODEX_SANDBOX.
//   Claude Code sets CLAUDECODE=1.
//   AI_AGENT ("claude-code_2-1-289_agent") is a cross-tool convention; its
//   first segment is the tool.
// Codex is checked first: started from inside Claude Code it inherits
// CLAUDECODE, and the innermost agent is the one publishing.
export function detectAgent(env = process.env) {
  if (env.BYAGENT_AGENT) return String(env.BYAGENT_AGENT).slice(0, 64);
  if (env.CODEX_THREAD_ID || env.CODEX_SESSION_ID || env.CODEX_SANDBOX) return "codex";
  if (env.CLAUDECODE === "1") return "claude-code";
  const tool = String(env.AI_AGENT || "").split("_")[0];
  if (/^[a-z0-9][a-z0-9.-]{0,40}$/.test(tool)) return tool;
  return "";
}

// keyName is what a connected agent's key is called in the dashboard, so a
// person can tell keys apart and revoke one: "claude-code on laptop".
export function keyName(env = process.env, host = hostname()) {
  const who = detectAgent(env) || "byagent-cli";
  const where = String(host || "").replace(/\.local$/, "");
  return (where ? `${who} on ${where}` : who).slice(0, 64);
}

let version = "";
export function cliVersion() {
  if (!version) {
    try {
      version = JSON.parse(readFileSync(new URL("../package.json", import.meta.url), "utf8")).version || "0";
    } catch {
      version = "0";
    }
  }
  return version;
}

// clientHeaders tell the server which CLI and which agent sent a request.
// They label versions in the history; they are never a credential.
export function clientHeaders(env = process.env) {
  const h = { "User-Agent": `byagent-cli/${cliVersion()}` };
  const agent = detectAgent(env);
  if (agent && /^[\x20-\x7e]{1,64}$/.test(agent)) h["X-Byagent-Client"] = agent;
  return h;
}
