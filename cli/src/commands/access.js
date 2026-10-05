import { apiJson } from "../client.js";
import { usage } from "../errors.js";

const USAGE = "usage: byagent access <id> [--public | --password | --restricted [--allow <email|@domain>]... | --add <email|@domain>... | --off] [--invite <email>...]";

// readPassword gets a page password without it ever touching argv, where it
// would land in shell history and the process list. Piped stdin wins, so an
// agent can run printf '%s' "$PW" | byagent access <id> --password; a person
// at a terminal is prompted twice with the echo off.
export async function readPassword({ stdin = process.stdin, stderr = process.stderr } = {}) {
  if (!stdin.isTTY) {
    let text = "";
    stdin.setEncoding("utf8");
    for await (const chunk of stdin) text += chunk;
    const pw = text.replace(/\r?\n$/, "");
    if (!pw) throw usage("--password reads the password from stdin: printf '%s' \"$PASSWORD\" | byagent ... --password");
    return pw;
  }
  const first = await promptHidden("Page password: ", { stdin, stderr });
  const again = await promptHidden("Again: ", { stdin, stderr });
  if (first !== again) throw usage("the two passwords did not match");
  return first;
}

function promptHidden(question, { stdin, stderr }) {
  return new Promise((resolve, reject) => {
    stderr.write(question);
    let buf = "";
    const done = (fn) => {
      stdin.removeListener("data", onData);
      stdin.setRawMode(false);
      stdin.pause();
      stderr.write("\n");
      fn();
    };
    const onData = (chunk) => {
      for (const c of String(chunk)) {
        if (c === "\r" || c === "\n" || c === "\u0004") return done(() => resolve(buf));
        if (c === "\u0003") return done(() => reject(usage("cancelled")));
        if (c === "\u007f" || c === "\b") buf = buf.slice(0, -1);
        else buf += c;
      }
    };
    stdin.setRawMode(true);
    stdin.setEncoding("utf8");
    stdin.resume();
    stdin.on("data", onData);
  });
}

// splitEntries sorts --allow / --add values into addresses and domains:
// "@acme.com" or a bare "acme.com" is a domain, anything else with an @ an
// address. Commas also separate, so --allow a@x.com,b@x.com works.
export function splitEntries(values) {
  const emails = [];
  const domains = [];
  for (const v of values || []) {
    for (const e of String(v).split(",").map((s) => s.trim()).filter(Boolean)) {
      if (e.startsWith("@") || !e.includes("@")) domains.push(e.replace(/^@/, ""));
      else emails.push(e);
    }
  }
  return { emails, domains };
}

export async function getAccess(cfg, id) {
  return apiJson(cfg, "GET", `/api/artifacts/${encodeURIComponent(id)}/access`);
}

export async function patchAccess(cfg, id, body) {
  return apiJson(cfg, "PATCH", `/api/artifacts/${encodeURIComponent(id)}/access`, {
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
}

// accessLines describes an access policy for people.
export function accessLines(a) {
  const lines = [];
  if (a.mode === "password") lines.push(`${a.id}: anyone with the password`);
  else if (a.mode === "restricted" && !a.allowed_emails.length && !a.allowed_domains.length) lines.push(`${a.id}: only your workspace`);
  else if (a.mode === "restricted") {
    lines.push(`${a.id}: only these people, who sign in with a code sent to their email`);
    for (const e of a.allowed_emails) lines.push(`  ${e}`);
    for (const d of a.allowed_domains) lines.push(`  @${d}`);
  } else if (a.share_code) lines.push(`${a.id}: anyone with the link, which carries the share code`);
  else lines.push(`${a.id}: anyone with the link`);
  if (a.share_url) lines.push(`share ${a.share_url}`);
  else lines.push(`${a.url}`);
  return lines;
}

export async function access(cfg, { positional, flags }) {
  const id = positional[0];
  if (!id) throw usage(USAGE);
  if (positional.length > 1) {
    throw usage("unexpected argument " + JSON.stringify(positional[1]) + "; a password is never taken from the command line, pipe it to --password on stdin");
  }
  const allow = flags.allow !== undefined;
  const add = flags.add !== undefined;
  const picked = [flags.public, flags.password, flags.restricted || allow, add, flags.off].filter(Boolean).length;
  if (picked > 1) throw usage("choose one of --public, --password, --restricted/--allow, --add or --off");

  let a;
  if (flags.public) a = await patchAccess(cfg, id, { mode: "anyone_with_link" });
  else if (flags.password) a = await patchAccess(cfg, id, { mode: "password", password: await readPassword() });
  else if (flags.off) a = await patchAccess(cfg, id, { mode: "restricted" });
  else if (flags.restricted || allow) {
    const { emails, domains } = splitEntries(flags.allow);
    a = await patchAccess(cfg, id, { mode: "restricted", allowed_emails: emails, allowed_domains: domains });
  } else if (add) {
    // The API replaces whole lists: read, merge, write.
    const cur = await getAccess(cfg, id);
    const keep = cur.mode === "restricted" ? cur : { allowed_emails: [], allowed_domains: [] };
    const { emails, domains } = splitEntries(flags.add);
    a = await patchAccess(cfg, id, {
      mode: "restricted",
      allowed_emails: [...keep.allowed_emails, ...emails],
      allowed_domains: [...keep.allowed_domains, ...domains],
    });
  }

  if (flags.invite !== undefined) {
    const res = await apiJson(cfg, "POST", `/api/artifacts/${encodeURIComponent(id)}/access/invites`, {
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ emails: flags.invite.flatMap((v) => String(v).split(",")).map((s) => s.trim()).filter(Boolean) }),
    });
    const n = res.notifications;
    const lines = [
      ...n.sent.map((e) => `invited ${e}`),
      ...n.skipped.map((s) => `skipped ${s.email}: ${s.reason === "not_allowed" ? "not on the list by name" : s.reason}`),
      ...n.failed.map((f) => `failed ${f.email}: ${f.error}`),
    ];
    return { json: a ? { ...a, notifications: n } : { id, notifications: n }, human: [...(a ? accessLines(a) : []), ...lines] };
  }
  if (!a) a = await getAccess(cfg, id);
  return { json: a, human: accessLines(a) };
}
