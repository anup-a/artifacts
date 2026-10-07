# Agent notes

- **Seeing a UI change:** `scripts/dev.sh` rebuilds and restarts the server on :8792. Run it after every edit: `static/` and `templates/` are embedded at build time, so a running server serves the old files. `node scripts/shots.mjs` screenshots it across widths and themes (first, `npm --prefix runtime ci`).
- **Merging to `main` deploys** (the CI `deploy` job). Open a PR and wait for the go-ahead before merging.
- **Review rules:** `CODING_STANDARDS.md`.
