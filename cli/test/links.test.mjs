import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { isDeployCommand, deployURL, deployOf } from "../src/deploys.js";
import { fileDeploy } from "../src/commands/hooks.js";
import { linkBody } from "../src/commands/links.js";

test("isDeployCommand knows deploys from everything else", () => {
  for (const c of [
    "vercel --prod", "npx vercel deploy --prod --yes", "VERCEL_ORG=x vercel .", "cd site && npx -y netlify-cli deploy --prod --dir dist",
    "wrangler pages deploy ./out", "npx wrangler deploy", "firebase deploy --only hosting", "flyctl deploy", "npx gh-pages -d dist",
    "pnpm run deploy", "npm run deploy:prod", "yarn deploy", "bash ~/.claude/skills/here-now/scripts/publish.sh ./site", "./deploy.sh",
  ]) assert.ok(isDeployCommand(c), c);
  for (const c of [
    "vercel logs app", "vercel env pull", "vercel ls", "curl https://a.vercel.app", "npm publish", "pnpm publish --access public",
    "netlify status", "wrangler dev", "npm run build", "git push origin main", "", undefined,
  ]) assert.ok(!isDeployCommand(c), String(c));
});

test("deployURL picks the address a person would share", () => {
  const vercel = "🔍  Inspect: https://vercel.com/anup/app/abc [2s]\n✅  Production: https://app-k2j3h4-anup.vercel.app [20s]\n🔗  Aliased: https://app.vercel.app [20s]";
  assert.equal(deployURL(vercel), "https://app.vercel.app");
  assert.equal(deployURL("Website URL:       https://creatica.netlify.app\nUnique deploy URL: https://6720abc--creatica.netlify.app"), "https://creatica.netlify.app");
  assert.equal(deployURL("✨ Deployment complete! Take a peek over at https://4f2a1b9c.myproj.pages.dev"), "https://myproj.pages.dev");
  assert.equal(deployURL("Preview: https://app-abc123-anup.vercel.app."), "https://app-abc123-anup.vercel.app");
  assert.equal(deployURL("pushed to https://github.com/anup-a/site and https://byagent.dev/x"), "");
});

test("deployOf reads Bash and MCP deploy calls and nothing else", () => {
  const bash = (command, stdout) => ({ tool_name: "Bash", tool_input: { command }, tool_response: { stdout, stderr: "" } });
  assert.equal(deployOf(bash("npx vercel --prod", "Aliased: https://app.vercel.app")), "https://app.vercel.app");
  assert.equal(deployOf(bash("curl -s https://app.vercel.app", "Aliased: https://app.vercel.app")), "");
  assert.equal(deployOf({ tool_name: "mcp__vercel__deploy_to_vercel", tool_response: [{ type: "text", text: "Live at https://site.vercel.app" }] }), "https://site.vercel.app");
  assert.equal(deployOf({ tool_name: "mcp__vercel__list_deployments", tool_response: { url: "https://site.vercel.app" } }), "");
  assert.equal(deployOf({ tool_name: "mcp__claude_ai_Vercel__create_deployment", tool_response: { url: "https://site-abc.vercel.app" } }), "https://site-abc.vercel.app");
  assert.equal(deployOf({ tool_name: "mcp__byagent__publish", tool_response: { url: "https://site.vercel.app" } }), "");
  assert.equal(deployOf({ tool_name: "Write", tool_input: { file_path: "x.html" } }), "");
});

test("linkBody carries the agent and the folder's collection", () => {
  const dir = mkdtempSync(join(tmpdir(), "byagent-link-"));
  writeFileSync(join(dir, ".byagent.json"), JSON.stringify({ collection: "creatica" }));
  assert.deepEqual(linkBody("https://a.vercel.app", {}, { CLAUDECODE: "1" }, dir), { url: "https://a.vercel.app", agent: "claude-code", project: "creatica" });
  assert.deepEqual(linkBody("https://a.vercel.app", { title: "Launch", project: "x", agent: "grok" }, {}, dir), { url: "https://a.vercel.app", agent: "grok", title: "Launch", project: "x" });
});

test("fileDeploy speaks once for a new link and stays quiet otherwise", async () => {
  const sent = [];
  const answer = (body) => async (url, init) => {
    sent.push({ url, init });
    return { ok: true, json: async () => body };
  };
  const input = { tool_name: "Bash", cwd: tmpdir(), tool_input: { command: "vercel --prod" }, tool_response: { stdout: "Aliased: https://app.vercel.app" } };
  const config = { api: "https://api.test", token: "ak_x" };
  const env = { CLAUDECODE: "1" };

  const out = await fileDeploy(input, { env, config, fetchImpl: answer({ created: true, url: "https://app.vercel.app/", provider: "Vercel" }) });
  assert.equal(out.systemMessage, "byagent: linked https://app.vercel.app/");
  assert.match(out.hookSpecificOutput.additionalContext, /does not host it/);
  assert.equal(sent[0].url, "https://api.test/api/links");
  assert.equal(JSON.parse(sent[0].init.body).url, "https://app.vercel.app");
  assert.equal(sent[0].init.headers["X-Byagent-Client"], "claude-code");

  assert.equal(await fileDeploy(input, { env, config, fetchImpl: answer({ created: false }) }), null, "a refresh says nothing");
  assert.equal(await fileDeploy({ ...input, tool_input: { command: "ls" } }, { env, config, fetchImpl: answer({ created: true }) }), null);
  assert.equal(sent.length, 2, "a non-deploy never reaches the server");
  assert.equal(await fileDeploy(input, { env, config, fetchImpl: async () => ({ ok: false }) }), null);
  // Nothing configured: no request at all.
  assert.equal(await fileDeploy(input, { env: {}, home: mkdtempSync(join(tmpdir(), "byagent-home-")), fetchImpl: answer({ created: true }) }), null);
});
