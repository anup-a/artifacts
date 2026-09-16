import { usage } from "./errors.js";

// Flags that never take a value. Anything else written as --name expects one,
// either as --name=value or as the next argument.
const BOOLEAN = new Set(["json", "open", "all", "help", "version", "comments", "no-comments"]);

export function parseArgs(argv) {
  const positional = [];
  const flags = {};
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg === "--") {
      positional.push(...argv.slice(i + 1));
      break;
    }
    if (!arg.startsWith("--")) {
      positional.push(arg);
      continue;
    }
    const eq = arg.indexOf("=");
    const name = eq === -1 ? arg.slice(2) : arg.slice(2, eq);
    if (!name) throw usage(`bad flag ${arg}`);
    if (BOOLEAN.has(name)) {
      if (eq !== -1) throw usage(`--${name} does not take a value`);
      flags[name] = true;
      continue;
    }
    const value = eq === -1 ? argv[++i] : arg.slice(eq + 1);
    if (value === undefined) throw usage(`--${name} needs a value`);
    flags[name] = value;
  }
  return { command: positional.shift(), positional, flags };
}
