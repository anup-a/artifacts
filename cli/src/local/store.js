import { createHash, randomBytes } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

// dataDir is where local mode keeps everything: the page index, every
// version's files by content hash, and the key the CLI uses. Nothing in it
// is ever sent anywhere.
export function dataDir(env = process.env) {
  return env.BYAGENT_LOCAL_DIR || join(homedir(), ".byagent", "local");
}

export const DEFAULT_PORT = 4790;

export function localPort(env = process.env) {
  const n = Number(env.BYAGENT_LOCAL_PORT || DEFAULT_PORT);
  return Number.isInteger(n) && n > 0 && n < 65536 ? n : DEFAULT_PORT;
}

// localToken is the key the CLI sends to the local server. It is made once
// and kept in a file only this user can read, so a page open in the browser
// (which can reach the server but not the file) cannot use the owner API.
export function localToken(dir) {
  const path = join(dir, "token");
  try {
    return readFileSync(path, "utf8").trim();
  } catch {}
  mkdirSync(dir, { recursive: true, mode: 0o700 });
  const token = "local_" + randomBytes(24).toString("hex");
  writeFileSync(path, token + "\n", { mode: 0o600 });
  return token;
}

const ALPHABET = "abcdefghijklmnopqrstuvwxyz0123456789";

export function newId(prefix = "", n = 12) {
  const bytes = randomBytes(n);
  let out = prefix;
  for (const b of bytes) out += ALPHABET[b % ALPHABET.length];
  return out;
}

export function sha256(buf) {
  return createHash("sha256").update(buf).digest("hex");
}

// Store keeps the index in one JSON file, rewritten whole through a rename so
// a crash never leaves half a file, and file bodies in blobs/<sha256>. Local
// mode is one person on one machine, so this is plenty.
export class Store {
  constructor(dir) {
    this.dir = dir;
    this.blobs = join(dir, "blobs");
    this.path = join(dir, "db.json");
    mkdirSync(this.blobs, { recursive: true, mode: 0o700 });
    this.db = { artifacts: {} };
    if (existsSync(this.path)) {
      const parsed = JSON.parse(readFileSync(this.path, "utf8"));
      if (parsed && typeof parsed.artifacts === "object") this.db = parsed;
    }
  }

  save() {
    const tmp = this.path + ".tmp";
    writeFileSync(tmp, JSON.stringify(this.db));
    renameSync(tmp, this.path);
  }

  hasBlob(hash) {
    return existsSync(join(this.blobs, hash));
  }

  putBlob(hash, body) {
    if (!this.hasBlob(hash)) writeFileSync(join(this.blobs, hash), body);
  }

  blob(hash) {
    return readFileSync(join(this.blobs, hash));
  }

  artifact(id) {
    return Object.hasOwn(this.db.artifacts, id) ? this.db.artifacts[id] : null;
  }

  all() {
    return Object.values(this.db.artifacts);
  }

  put(a) {
    this.db.artifacts[a.id] = a;
    this.save();
  }

  // remove deletes a page and then every file body no other page uses.
  remove(id) {
    const gone = this.artifact(id);
    delete this.db.artifacts[id];
    this.save();
    if (!gone) return;
    const used = new Set(this.all().flatMap((a) => a.versions.flatMap((v) => v.files.map((f) => f.sha256))));
    for (const v of gone.versions) {
      for (const f of v.files) {
        if (!used.has(f.sha256)) rmSync(join(this.blobs, f.sha256), { force: true });
      }
    }
  }
}
