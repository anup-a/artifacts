import { parseArgs } from "./args.js";
import { resolveConfig, trimSlash } from "./config.js";
import { CliError, usage } from "./errors.js";
import { publish } from "./commands/publish.js";
import { get, list, versions, rollback, remove } from "./commands/artifacts.js";
import { comments, reply, resolve } from "./commands/comments.js";
import { login } from "./commands/login.js";
import { whoami, portalLink } from "./commands/account.js";

// needsConfig is false for login, which builds its own config from its flags.
const COMMANDS = {
  login: { run: login, needsConfig: false },
  whoami: { run: whoami, needsConfig: true },
  "portal-link": { run: portalLink, needsConfig: true },
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

const help = (api) => `artifacts - publish a page, share the URL, read the comments on it

Usage:
  artifacts login --api <url> [--token <key>]
  artifacts publish <dir|file.html> [--id <id>] [--title <t>] [--end-user <id>]
                    [--comments|--no-comments] [--project <name>] [--tag <t>]...
                    [--cover <file>] [--cover-style dots|grid|lines|wash|rings|plain]
  artifacts get <id>
  artifacts list [--end-user <id>] [--project <name>]
  artifacts whoami
  artifacts portal-link <end-user id>
  artifacts versions <id>
  artifacts rollback <id> <version>
  artifacts comments <id> [--open|--all] [--since <rfc3339>]
  artifacts reply <id> <thread> "<text>"
  artifacts resolve <id> <thread>
  artifacts delete <id>

Every command takes --json and then prints exactly one JSON object.
Config comes from ARTIFACTS_TOKEN / ARTIFACTS_API, else ~/.artifacts/config.json.
Create a key at ${api}/app/keys, then paste it with login or pipe it on stdin.
POST /api/keys was removed. Keys belong to the workspace selected in the app.
--end-user attributes or filters an opaque customer id. Publish and list JSON
include end_user. portal-link is for integrator workspaces; links last 7 days.
--no-comments hides the comment panel from viewers and closes the viewer
comment API for that artifact; --comments turns it back on. Without either,
a republish keeps the current setting (new artifacts start with comments on).
Artifact JSON carries comments_enabled.
--project (1-64 chars) and --tag (repeatable, 1-32 chars, up to 10) label the
artifact for the dashboard gallery; list --project filters by label. The
directory's .artifacts.json remembers project and tags, so a republish keeps
them. --cover names an image (svg, png, jpg, webp) shown as the card cover and
og:image: a file inside the directory is used in place, any other file is
copied in as _cover.<ext>. Without a cover the server draws a quiet OG-style
card (title, project, host and date over a subtle pattern) in the
--cover-style of your choice (default: derived from the id). Artifact JSON
carries project, tags, cover_url, cover_style and published_at.

Server sign-in: KEY_SIGNING_SECRET plus GITHUB_CLIENT_ID / GITHUB_CLIENT_SECRET
or EMAIL_PROVIDER=log|resend. Resend uses RESEND_API_KEY and EMAIL_FROM.
The artifact quota is LIMIT_ARTIFACTS_PER_TENANT; LIMIT_ARTIFACTS_PER_KEY is a
deprecated fallback.`;

export async function main(argv, { out = console.log, err = console.error, env = process.env } = {}) {
  let json = false;
  try {
    const { command, positional, flags } = parseArgs(argv);
    json = Boolean(flags.json);
    if (!command || flags.help || command === "help") {
      out(help(trimSlash(flags.api || env.ARTIFACTS_API || "<PUBLIC_API_ORIGIN>")));
      return command || flags.help ? 0 : 2;
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
