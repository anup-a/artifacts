import { apiJson } from "../client.js";
import { CliError, usage } from "../errors.js";

const USAGE = "usage: byagent vars | vars set NAME --upstream <host> [--upstream <host>]... (value on stdin) | vars rm NAME";

// vars manages account variables: secrets a page's proxy routes send to an
// API, added on the server so the browser never sees them. Values are
// write-only; list shows names and the hosts each may be sent to.
export async function vars(cfg, ctx) {
  const [sub, name, ...extra] = ctx.positional;
  if (!sub || sub === "list" || sub === "ls") return list(cfg);
  if (extra.length) throw usage(USAGE);
  if (sub === "set") return set(cfg, name, ctx);
  if (sub === "rm" || sub === "delete") return rm(cfg, name);
  throw usage(USAGE);
}

async function list(cfg) {
  const res = await apiJson(cfg, "GET", "/api/variables");
  const rows = res.variables || [];
  return {
    json: res,
    human: rows.length
      ? rows.map((v) => `${v.name}  ${(v.allowed_upstreams || []).join(", ")}  updated ${v.updated_at}`)
      : ["no variables"],
  };
}

// upstreamsOf accepts --upstream more than once or a comma-separated list.
export function upstreamsOf(flags) {
  const raw = Array.isArray(flags.upstream) ? flags.upstream : flags.upstream ? [flags.upstream] : [];
  return raw.flatMap((v) => String(v).split(",")).map((v) => v.trim()).filter(Boolean);
}

async function set(cfg, name, { flags, env = process.env, stdin = process.stdin, stderr = process.stderr }) {
  if (!name) throw usage(USAGE);
  if (flags.value !== undefined) {
    throw usage("pass the value on stdin (or --from-env VAR), never as an argument: arguments end up in shell history and the process list");
  }
  const upstreams = upstreamsOf(flags);
  if (!upstreams.length) {
    throw usage("--upstream <host> is required: the API host this value may be sent to, for example --upstream openrouter.ai");
  }
  let value;
  if (flags["from-env"] !== undefined) {
    value = env[flags["from-env"]];
    if (value === undefined) throw usage(`environment variable ${flags["from-env"]} is not set`);
  } else {
    value = await readSecret(stdin, stderr);
  }
  value = String(value).trim();
  if (!value) throw usage("no value: pipe it on stdin, for example: printf %s \"$KEY\" | byagent vars set NAME --upstream api.example.com");
  const res = await apiJson(cfg, "PUT", `/api/variables/${encodeURIComponent(name)}`, {
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ value, allowed_upstreams: upstreams }),
  });
  return {
    json: res,
    human: [`saved ${res.name}, sent only to ${(res.allowed_upstreams || []).join(", ")}`],
  };
}

async function rm(cfg, name) {
  if (!name) throw usage(USAGE);
  const res = await apiJson(cfg, "DELETE", `/api/variables/${encodeURIComponent(name)}`);
  return { json: res, human: [`deleted ${name}`] };
}

// readSecret reads the value from piped stdin, or prompts with the typing
// hidden when stdin is a terminal.
async function readSecret(stdin, stderr) {
  if (!stdin.isTTY) {
    const chunks = [];
    for await (const chunk of stdin) chunks.push(chunk);
    return Buffer.concat(chunks.map((c) => (typeof c === "string" ? Buffer.from(c) : c))).toString("utf8");
  }
  return new Promise((resolve, reject) => {
    stderr.write("Value (hidden): ");
    let value = "";
    const done = () => {
      stdin.off("data", onData);
      stdin.setRawMode(false);
      stdin.pause();
      stderr.write("\n");
    };
    const onData = (chunk) => {
      for (const ch of String(chunk)) {
        if (ch === "\r" || ch === "\n") {
          done();
          resolve(value);
          return;
        }
        if (ch === "\u0003") {
          done();
          reject(new CliError("cancelled", { exit: 2, code: "USAGE" }));
          return;
        }
        if (ch === "\u007f" || ch === "\b") value = value.slice(0, -1);
        else value += ch;
      }
    };
    stdin.setRawMode(true);
    stdin.setEncoding("utf8");
    stdin.on("data", onData);
    stdin.resume();
  });
}
