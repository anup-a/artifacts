import { spawn } from "node:child_process";
import { mkdirSync, openSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { CliError, usage } from "../errors.js";
import { dataDir, localPort, localToken } from "../local/store.js";

const USAGE = "usage: byagent local serve|start|stop|status [--port <n>]";

function portOf(flags, env) {
  if (flags.port === undefined) return localPort(env);
  const n = Number(flags.port);
  if (!Number.isInteger(n) || n < 1 || n > 65535) throw usage("--port must be a port number");
  return n;
}

const pidFile = (dir) => join(dir, "server.pid");

async function health(port) {
  try {
    const res = await fetch(`http://127.0.0.1:${port}/api/health`, { signal: AbortSignal.timeout(1000) });
    const body = await res.json();
    return res.ok && body && body.local ? body : null;
  } catch {
    return null;
  }
}

// startServer launches `byagent local serve` in the background, detached so
// it outlives this command, and waits until it answers.
async function startServer(port, env) {
  const dir = dataDir(env);
  mkdirSync(dir, { recursive: true, mode: 0o700 });
  const log = openSync(join(dir, "server.log"), "a");
  const bin = fileURLToPath(new URL("../../bin/byagent.js", import.meta.url));
  const child = spawn(process.execPath, [bin, "local", "serve", "--port", String(port)], {
    detached: true,
    stdio: ["ignore", log, log],
    env: { ...env, BYAGENT_LOCAL_DIR: dir },
  });
  child.unref();
  for (let i = 0; i < 50; i++) {
    if (await health(port)) return;
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new CliError(`the local server did not start on port ${port}; see ${join(dir, "server.log")}`, { code: "LOCAL_START" });
}

// localConfig is the config every command gets under --local: the local
// server's address and key. The server is started on first use.
export async function localConfig(flags, env) {
  const port = portOf(flags, env);
  const dir = dataDir(env);
  const up = await health(port);
  if (up && up.data_dir !== dir) {
    throw new CliError(`port ${port} is serving ${up.data_dir}, not ${dir}; set BYAGENT_LOCAL_PORT`, { code: "LOCAL_PORT" });
  }
  if (!up) await startServer(port, env);
  return { api: `http://localhost:${port}`, token: localToken(dir), local: true };
}

async function serve(flags, env) {
  const port = portOf(flags, env);
  const dir = dataDir(env);
  const { createLocalServer } = await import("../local/server.js");
  const server = createLocalServer({ dir, port });
  await new Promise((resolve, reject) => {
    server.once("error", (e) => reject(e.code === "EADDRINUSE" ? new CliError(`port ${port} is in use; try --port`, { code: "LOCAL_PORT" }) : e));
    server.listen(port, "127.0.0.1", resolve);
  });
  writeFileSync(pidFile(dir), String(process.pid));
  const stop = () => {
    try {
      if (readFileSync(pidFile(dir), "utf8").trim() === String(process.pid)) rmSync(pidFile(dir));
    } catch {}
    process.exit(0);
  };
  process.on("SIGINT", stop);
  process.on("SIGTERM", stop);
  console.error(`byagent local: http://localhost:${port}/ (data in ${dir})`);
  // Serve until stopped; main() never gets control back.
  await new Promise(() => {});
}

export async function local(_cfg, { positional, flags, env = process.env }) {
  const sub = positional[0];
  const port = portOf(flags, env);
  const dir = dataDir(env);
  const home = `http://localhost:${port}/`;
  if (sub === "serve") return serve(flags, env);
  if (sub === "start") {
    if (!(await health(port))) await startServer(port, env);
    return { json: { running: true, url: home, data_dir: dir }, human: [home, `pages are kept in ${dir}`] };
  }
  if (sub === "status") {
    const up = await health(port);
    return {
      json: { running: Boolean(up), url: home, data_dir: up ? up.data_dir : dir },
      human: [up ? `running at ${home}` : `not running (byagent local start, or any command with --local)`, `pages are kept in ${up ? up.data_dir : dir}`],
    };
  }
  if (sub === "stop") {
    let pid = 0;
    try {
      pid = Number(readFileSync(pidFile(dir), "utf8").trim());
    } catch {}
    if (!pid || !(await health(port))) return { json: { running: false }, human: ["not running"] };
    try {
      process.kill(pid, "SIGTERM");
    } catch {}
    return { json: { running: false, stopped: pid }, human: ["stopped"] };
  }
  throw usage(USAGE);
}
