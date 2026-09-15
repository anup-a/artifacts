// CliError is the only error the CLI prints. Everything else is a bug and gets
// an INTERNAL envelope so a caller parsing --json output never sees a stack.
export class CliError extends Error {
  constructor(message, { exit = 1, code = "ERROR", status = 0, requestId = "" } = {}) {
    super(message);
    this.name = "CliError";
    this.exit = exit;
    this.code = code;
    this.status = status;
    this.requestId = requestId;
  }
}

// usage errors exit 2 so scripts can tell "you called me wrong" from "the call failed".
export function usage(message) {
  return new CliError(message, { exit: 2, code: "USAGE" });
}
