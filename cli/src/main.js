import { parseArgs } from "./args.js";
import { resolveConfig, trimSlash } from "./config.js";
import { createGuest } from "./guest.js";
import { CliError, usage } from "./errors.js";
import { publish } from "./commands/publish.js";
import { get, list, share, versions, rollback, remove, expire } from "./commands/artifacts.js";
import { comments, reply, resolve, mark } from "./commands/comments.js";
import { collections, collection } from "./commands/collections.js";
import { login } from "./commands/login.js";
import { whoami, portalLink } from "./commands/account.js";
import { hooks } from "./commands/hooks.js";
import { brief } from "./commands/brief.js";
import { version, diff } from "./commands/artifacts.js";
import { analytics } from "./commands/analytics.js";
import { access } from "./commands/access.js";
import { vars } from "./commands/vars.js";
import { domains, domain } from "./commands/domains.js";
import { local, localConfig } from "./commands/local.js";

// needsConfig is false for login, which builds its own config from its flags.
const COMMANDS = {
  login: { run: login, needsConfig: false },
  whoami: { run: whoami, needsConfig: true },
  "portal-link": { run: portalLink, needsConfig: true },
  publish: { run: publish, needsConfig: true },
  get: { run: get, needsConfig: true },
  list: { run: list, needsConfig: true },
  collections: { run: collections, needsConfig: true },
  collection: { run: collection, needsConfig: true },
  share: { run: share, needsConfig: true },
  versions: { run: versions, needsConfig: true },
  rollback: { run: rollback, needsConfig: true },
  version: { run: version, needsConfig: true },
  diff: { run: diff, needsConfig: true },
  delete: { run: remove, needsConfig: true },
  expire: { run: expire, needsConfig: true },
  access: { run: access, needsConfig: true },
  comments: { run: comments, needsConfig: true },
  reply: { run: reply, needsConfig: true },
  resolve: { run: resolve, needsConfig: true },
  mark: { run: mark, needsConfig: true },
  hooks: { run: hooks, needsConfig: false },
  brief: { run: brief, needsConfig: true },
  analytics: { run: analytics, needsConfig: true },
  vars: { run: vars, needsConfig: true },
  domains: { run: domains, needsConfig: true },
  domain: { run: domain, needsConfig: true },
  local: { run: local, needsConfig: false },
};

const help = (api) => `byagent - publish a page, share the URL, read the comments on it

Usage:
  byagent login --code <code> [--api <url>] [--name <key name>]
  byagent login --browser [--no-wait] [--timeout <seconds>] [--api <url>] [--name <key name>]
  byagent login --wait [--timeout <seconds>]
  byagent login --api <url> [--token <key>]
  byagent login --guest
  byagent publish <dir|file.html|file.md> [--id <id>] [--title <t>] [--end-user <id>]
                  [--private|--public]
                  [--comments|--no-comments] [--project <name>] [--tag <t>]...
                  [--cover <file>] [--cover-style dots|grid|lines|wash|rings|plain]
                  [--lang <tag>]
                  [--agent <name>] [--model <name>] [--expires <when>]
                  [--password]
  byagent get <id>
  byagent share <id> [--off] [--expires <when>]
  byagent expire <id> <when|never>
  byagent access <id> [--public | --password | --restricted --allow <email|@domain>...
                      | --add <email|@domain>... | --off] [--invite <email>...]
  byagent list [--search <text>] [--end-user <id>] [--project <name>]
  byagent collections
  byagent collection <name>
  byagent whoami
  byagent portal-link <end-user id>
  byagent versions <id>
  byagent rollback <id> <version>
  byagent version rm <id> <version>
  byagent diff <id> <from> [<to>]
  byagent comments <id> [--open|--all] [--since <rfc3339>]
  byagent comments <id> --wait [--timeout <seconds>] [--interval <seconds>] [--since <rfc3339>]
  byagent reply <id> <thread> "<text>"
  byagent resolve <id> <thread>
  byagent mark <id> <thread> seen|working|declined [--note "<text>"]
  byagent delete <id>
  byagent hooks install|uninstall claude|codex [--scope user|project]
  byagent brief [push [--private|--public] [--project <name>]]
  byagent analytics [<id>] [--range 24h|7d|30d|90d|all]
  byagent vars
  byagent vars set NAME --upstream <host> [--upstream <host>]... [--from-env VAR]
  byagent vars rm NAME
  byagent domains
  byagent domain add <host> [--page <id>] [--path /x] [--primary]
  byagent domain <host>
  byagent domain rm <host>
  byagent domain unmount <host> [--path /x]
  byagent domain primary <id> <host> [--path /x] | --off
  byagent local start|stop|status|serve [--port <n>]
  byagent <command> --local

A .md file (or a directory whose index is index.md / README.md) is rendered to
a styled page on the way in; its first H1 becomes the title unless --title is
given, and images or links relative to it keep working. Its language (Chinese,
Japanese, Korean or English) is detected from the text; --lang zh-CN overrides.
Every command takes --json and then prints exactly one JSON object.
Config comes from ARTIFACTS_TOKEN / ARTIFACTS_API, else ~/.artifacts/config.json.
Connecting an agent never needs a key in the chat. login --code takes the
one-time code from a prompt made on the dashboard's Connect agent page
(${api}/app/connect); login --browser prints a link where a signed-in person
approves this agent. Either way the agent gets a key of its own, named after it
("claude-code on laptop", or --name), which the dashboard can revoke. Codes work
once and expire in 10 minutes. login --browser --no-wait prints the link and
exits; login --wait picks the same request up again (exit 3 while not approved).
For CI or a script, create a key at ${api}/app/keys and pipe it to login.
With nothing configured, publish gets a guest key from https://app.byagent.dev
(or ARTIFACTS_API) and saves it: no account needed. Guest pages are public,
three at most, 5 MB each, and stop working 24 hours after the key was made.
Publish prints a claim_url: open it, sign in and confirm to keep the pages;
the guest key stops working then, and the dashboard offers a connect prompt
(or run login --browser). login --guest gets a guest key up front;
BYAGENT_GUEST=0 disables it.
POST /api/keys was removed. Keys belong to the workspace selected in the app.
--end-user attributes or filters an opaque customer id. Publish and list JSON
include end_user. portal-link is for integrator workspaces; links last 7 days.
--no-comments hides the comment panel from viewers and closes the viewer
comment API for that artifact; --comments turns it back on. Without either,
a republish keeps the current setting (new artifacts start with comments on).
Artifact JSON carries comments_enabled.
comments --wait blocks until a reader leaves a new comment (a new thread or a
reply in an old one), then prints the threads holding it and exits; owner
replies never wake it. It polls every --interval seconds (default 20) and gives
up after --timeout seconds (default 1800), printing timed_out: true. Run it in
the background to have the agent pick up comments while it works.
share issues a six-digit code for a private artifact and prints it with a
one-click link; running it again rotates the code, which stops every link that
carried the old one. share --off withdraws it. Anyone holding the code can open
and comment on that artifact, and nothing else, without signing in. get shows
the code; list never does.
--agent and --model record what produced a version (free text, 64 chars),
else BYAGENT_AGENT / BYAGENT_MODEL. Without them the agent is detected from
its environment (Claude Code, Codex, or an AI_AGENT variable) and sent with
every request; versions shows it with the publishing key's name.
versions marks the live version and prints a preview link per version: it
opens that version without making it live, for signed-in members of the
workspace only. rollback makes an old version live again (v3 or 3): nothing
is re-uploaded, and access, share code, expiry and comments stay as they are;
the next publish still gets a new number. version rm deletes one old version
for good (its link stops working, its files are erased once nothing else uses
them); the live version cannot be deleted. diff lists files added, removed and
changed between two versions; <to> defaults to the live one.
<when> is a duration from now (30m, 12h, 7d, 2w), an RFC 3339 time, or never.
expire (or publish --expires) makes a page stop working at that moment; it
then answers 410 and is erased like a delete. expire <id> never keeps it.
share --expires ends the share code at that moment; the page stays private.
access shows or sets who can open a page. --public: anyone with the link
(the page becomes public). --password: anyone with the password; it is read
from stdin (printf '%s' "$PW" | byagent access <id> --password) or asked
for twice at a terminal, never taken from argv, and the page goes private.
Changing it signs every visitor out. --restricted --allow sam@acme.com
--allow @acme.com: only those people; a visitor enters their email, gets a
one-time code, and needs no account. --allow replaces the whole list; --add
adds to it. --off: only your workspace. --invite emails people on the list
by name a link to the page (never a whole @domain). publish --password
publishes privately, then sets the password. Readers who get in can comment.
mark shows readers what is happening to their comment: seen, working, or
declined. declined needs --note with the reason; it posts the note as a reply
and resolves the thread. --note works with seen and working too. Thread JSON
carries progress ("" until marked).
delete is permanent: the page, its versions, comments and view counts are
erased, and the id only ever answers 410 after.
--private restricts viewing to the signed-in owning workspace; --public makes
the artifact publicly accessible. Omit both to preserve visibility on republish;
new artifacts default to public. Artifact JSON carries visibility. Private URLs
open on the app origin and require sign-in; private pages have no viewer comment
panel. Owner comments remain available in the dashboard and API.
--project (1-64 chars) and --tag (repeatable, 1-32 chars, up to 10) label the
artifact for the dashboard gallery; list --project filters by label. The
directory's .artifacts.json remembers project and tags, so a republish keeps
them. --cover names an image (svg, png, jpg, webp) shown as the card cover and
og:image: a file inside the directory is used in place, any other file is
copied in as _cover.<ext>. Without a cover the server draws a quiet OG-style
card (title, project, host and date over a subtle pattern) in the
--cover-style of your choice (default: derived from the id). Artifact JSON
carries project, tags, cover_url, cover_style and published_at.
Every artifact sharing a --project label forms a collection. collections lists
them (name, count, last update, public share URL if shared), most recently
active first; collection <name> lists one collection's artifacts in reading
order: its saved order first, then by publish time. Reuse the exact same
--project string for pages that belong together.
hooks install adds a PostToolUse hook to Claude Code (~/.claude/settings.json)
or Codex (~/.codex/hooks.json); --scope project writes ./.claude or ./.codex
instead. After the agent writes a .html or .md page, the hook adds a short
note to its context suggesting byagent publish, or a republish when the
directory is already bound. It never publishes. Each file is mentioned once
per session, five at most; BYAGENT_NUDGE=0 silences it. byagent must be on
PATH (npm install -g byagent). hooks uninstall removes only this hook.
A .byagent.json at a project root, {"collection": "<name>"}, makes that
collection the default --project for every publish below it; the nearest file
wins. Commit it: it holds no key.
brief prints the folder's BRIEF.md (next to .byagent.json, else here), its
published version and the open comments on it; with no local BRIEF.md it
reads the pushed copy back. brief push publishes BRIEF.md
into the collection, tagged brief, updating the same page every time; a new
brief starts private. An unchanged file is not sent again. Briefs are for the
next agent or person to pick the work up: push one when the work changes
state (started, blocked, handed off, done), not after every turn.
analytics shows how many people viewed a page (with an id) or every page in
the workspace (without) over --range (default 30d): views, unique visitors,
a daily series (hourly for 24h), and the top referrers, countries, paths
and devices; the workspace report adds the top pages. Bots, link previews,
HEAD requests, assets and the dashboard preview are not counted. Visitors
are counted per page per day from a hash that changes daily and is never
stored, so a range's visitors add up the days. No cookies are used.
vars manages account variables: API keys a page's proxy routes use without
the key reaching the browser. vars set reads the value from stdin (piped, or
typed hidden at a prompt), or from --from-env VAR; never from an argument.
--upstream names the hosts the value may be sent to (and their subdomains).
A page declares routes in .byagent/proxy.json and calls _proxy/<route>; the
server adds the key and forwards the call. vars lists names and hosts only;
values are never shown again. vars rm deletes one.
domain add connects a hostname you own to the workspace and prints two DNS
records: a CNAME that routes it to byagent and a TXT record at _byagent.<host>
that proves it is yours. Nothing is served until the TXT record is seen;
byagent domain <host> checks again and shows the status. --page serves a page
at --path on it (default /, the root); several pages can share a domain at
different paths. A page keeps its visibility, share code and comments there.
domain primary makes the page's byagent.dev link 308-redirect to the domain
(active domains only); --off stops that. domain rm disconnects the domain.
No account (guest key), no custom domains.
--local (or BYAGENT_LOCAL=1) runs a command against a server on this
machine instead of byagent.dev: no account, no key, nothing uploaded.
publish, get, list, versions, rollback, version rm, diff, delete, comments
(with --wait), reply, resolve and mark all work; pages open at
http://localhost:4790/a/<id>/ with the comment panel, and
http://localhost:4790/ lists them. The first --local command starts the
server in the background; local stop ends it. Pages, versions and comments
live in ~/.byagent/local (BYAGENT_LOCAL_DIR); the port is 4790
(BYAGENT_LOCAL_PORT or --port). A directory published locally keeps its
local id apart from its byagent.dev one, so publishing it again without
--local makes a real link. Sharing, access, expiry, analytics, vars,
domains and collections need byagent.dev. local serve runs the server in
the foreground.

Server sign-in: KEY_SIGNING_SECRET plus GITHUB_CLIENT_ID / GITHUB_CLIENT_SECRET
or EMAIL_PROVIDER=log|resend. Resend uses RESEND_API_KEY and EMAIL_FROM.
The artifact quota is LIMIT_ARTIFACTS_PER_TENANT; LIMIT_ARTIFACTS_PER_KEY is a
deprecated fallback.`;

// configFor resolves the saved config. A publish with nothing configured gets
// a guest key instead of an error, so the first page needs no account;
// BYAGENT_GUEST=0 turns that off.
async function configFor(command, flags, env) {
  if (flags.local || env.BYAGENT_LOCAL === "1") return localConfig(flags, env);
  try {
    return resolveConfig({ env });
  } catch (e) {
    if (command !== "publish" || e.code !== "NOT_CONFIGURED" || env.BYAGENT_GUEST === "0" || env.ARTIFACTS_TOKEN) throw e;
    return (await createGuest({ env })).cfg;
  }
}

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

    const cfg = entry.needsConfig ? await configFor(command, flags, env) : null;
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
