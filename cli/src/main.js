import { parseArgs } from "./args.js";
import { resolveConfig } from "./config.js";
import { CliError, usage } from "./errors.js";
import { publish } from "./commands/publish.js";
import { get, list, versions, rollback, remove } from "./commands/artifacts.js";
import { comments, reply, resolve } from "./commands/comments.js";
import { login } from "./commands/login.js";

// needsConfig is false for login, which builds its own config from its flags.
const COMMANDS = {
  login: { run: login, needsConfig: false },
  publish: { run: publish, needsConfig: true },
  get: { run: get, needsConfig: true },
  list: { run: list, needsConfig: true },
  versions: { run: versions, needsConfig: true },
  rollback: { run: rollback, needsConfig: true },
  delete: { run: remove, needsConfig: true },
  comments: { run: comments, needsConfig: true },
  reply: { run: reply, needsConfig: true },
  resolve: { run: resolve, needsConfig: true },
};

const USAGE = `artifacts - publish a page, share the URL, read the comments on it

Usage:
  artifacts login --api <url> [--token <key>]
  artifacts publish <dir|file.html> [--id <id>] [--title <t>]
  artifacts get <id>
  artifacts list
  artifacts versions <id>
  artifacts rollback <id> <version>
  artifacts comments <id> [--open|--all] [--since <rfc3339>]
  artifacts reply <id> <thread> "<text>"
  artifacts resolve <id> <thread>
  artifacts delete <id>

Every command takes --json and then prints exactly one JSON object.
Config comes from ARTIFACTS_TOKEN / ARTIFACTS_API, else ~/.artifacts/config.json.`;

export async function main(argv, { out = console.log, err = console.error, env = process.env } = {}) {
  let json = false;
  try {
    const { command, positional, flags } = parseArgs(argv);
    json = Boolean(flags.json);
    if (!command || flags.help || command === "help") {
      out(USAGE);
      return command ? 0 : 2;
    }
    const entry = COMMANDS[command];
    if (!entry) throw usage(`unknown command ${command}`);

    const cfg = entry.needsConfig ? resolveConfig({ env }) : null;
    const result = await entry.run(cfg, { positional, flags, env });
    if (json) out(JSON.stringify(result.json));
    else for (const line of result.human) out(line);
    return 0;
  } catch (e) {
    const cliErr = e instanceof CliError ? e : new CliError(e.message, { code: "INTERNAL" });
    if (json) {
      err(JSON.stringify({ error: { code: cliErr.code, message: cliErr.message, request_id: cliErr.requestId || undefined } }));
    } else {
      err(`error: ${cliErr.message}${cliErr.requestId ? ` (request ${cliErr.requestId})` : ""}`);
    }
    return cliErr.exit;
  }
}
