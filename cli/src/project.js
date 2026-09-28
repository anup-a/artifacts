import { readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { CliError } from "./errors.js";

// PROJECT_FILE is the per-folder config a user commits at a project's root.
// It holds no secrets (the key stays in ~/.artifacts/config.json), so every
// agent working in the folder, on any machine, lands in the same collection.
export const PROJECT_FILE = ".byagent.json";

const MAX_COLLECTION_CHARS = 64;

// findProject returns { root, path, collection } for the nearest
// .byagent.json at or above start, or null when there is none. The nearest
// file wins, so a monorepo can give each app its own collection.
export function findProject(start = process.cwd()) {
  let dir = resolve(start);
  for (;;) {
    const path = join(dir, PROJECT_FILE);
    let raw = null;
    try {
      raw = readFileSync(path, "utf8");
    } catch (err) {
      if (err.code !== "ENOENT" && err.code !== "ENOTDIR") {
        throw new CliError(`cannot read ${path}: ${err.message}`, { exit: 2, code: "CONFIG" });
      }
    }
    if (raw !== null) return { root: dir, path, collection: collectionOf(raw, path) };
    const up = dirname(dir);
    if (up === dir) return null;
    dir = up;
  }
}

function collectionOf(raw, path) {
  let parsed;
  try {
    parsed = JSON.parse(raw);
  } catch (err) {
    throw new CliError(`cannot read ${path}: ${err.message}`, { exit: 2, code: "CONFIG" });
  }
  const c = parsed && typeof parsed.collection === "string" ? parsed.collection.trim() : "";
  if (!c || c.length > MAX_COLLECTION_CHARS) {
    throw new CliError(`${path} needs "collection": a name of 1 to ${MAX_COLLECTION_CHARS} characters`, { exit: 2, code: "CONFIG" });
  }
  return c;
}
