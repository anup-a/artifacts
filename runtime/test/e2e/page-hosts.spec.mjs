// Page hosts in a real browser: with CONTENT_DOMAIN set every page is served
// from {slug}.page.localhost, its own origin, and the browser itself keeps
// one page's script away from another page. Chromium resolves every
// *.localhost name to the machine, so no DNS is needed.
import { test, expect } from "@playwright/test";
import { spawn, execFileSync } from "node:child_process";
import { mkdtempSync, writeFileSync, readFileSync, mkdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const REPO = resolve(process.cwd(), "..");
// comments.spec.mjs owns ARTIFACTS_E2E_PORT (default 8099); dashboard and
// collections take the two ports below it, this suite the third.
const PORT = Number(process.env.ARTIFACTS_E2E_PORT || 8099) - 3;
const BASE = `http://127.0.0.1:${PORT}`;
const CONTENT = `page.localhost:${PORT}`;
const TOKEN = "e2e-pages-token";

let server;

/** Publish a directory of files; returns the publish output (id, url). */
function publish(files, id, { visibility, manifest } = {}) {
  const dir = mkdtempSync(join(tmpdir(), "artifacts-e2e-"));
  for (const [name, body] of Object.entries(files)) writeFileSync(join(dir, name), body);
  const args = ["run", "./scripts/e2e-publish.go", BASE, TOKEN, dir];
  if (id) args.push(id);
  const env = { ...process.env };
  if (visibility) env.ARTIFACT_VISIBILITY = visibility;
  if (manifest) env.ARTIFACT_MANIFEST = JSON.stringify(manifest);
  return JSON.parse(execFileSync("go", args, { cwd: REPO, encoding: "utf8", env }));
}

async function api(path, init = {}) {
  const res = await fetch(`${BASE}${path}`, {
    ...init,
    headers: { Authorization: `Bearer ${TOKEN}`, "Content-Type": "application/json", ...(init.headers || {}) },
  });
  if (!res.ok) throw new Error(`${path}: ${res.status}`);
  return res.json();
}

async function selectText(page, selector) {
  await page.evaluate((sel) => {
    const node = document.querySelector(sel).firstChild;
    const r = document.createRange();
    r.setStart(node, 0);
    r.setEnd(node, node.nodeValue.length);
    const s = window.getSelection();
    s.removeAllRanges();
    s.addRange(r);
  }, selector);
  const box = await page.locator(selector).boundingBox();
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.up();
}

test.beforeAll(async () => {
  const dataDir = mkdtempSync(join(tmpdir(), "artifacts-pages-"));
  mkdirSync(join(dataDir, "blobs"), { recursive: true });
  execFileSync("go", ["build", "-o", join(dataDir, "server"), "./cmd/artifacts-server"], { cwd: REPO });
  server = spawn(join(dataDir, "server"), [], {
    env: {
      ...process.env,
      DB: "sqlite",
      STORAGE: "local",
      DATA_DIR: dataDir,
      ADMIN_TOKEN: TOKEN,
      ADDR: `127.0.0.1:${PORT}`,
      PUBLIC_SHARE_ORIGIN: BASE,
      PUBLIC_API_ORIGIN: BASE,
      CONTENT_DOMAIN: `http://${CONTENT}`,
      LIMIT_PUBLISH_PER_MIN: "1000",
      LIMIT_COMMENTS_PER_MIN: "1000",
    },
    stdio: "ignore",
  });
  for (let i = 0; i < 100; i++) {
    try {
      if ((await fetch(`${BASE}/healthz`)).ok) return;
    } catch {
      /* not up yet */
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error("server did not become healthy");
});

test.afterAll(() => {
  if (server) server.kill();
});

test("a page lives on its own host, comments work there, and old links follow it", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const a = publish({ "index.html": html });
  expect(a.url).toMatch(new RegExp(`^http://[a-z2-7]{16}\\.${CONTENT.replace(".", "\\.")}/$`));

  // The old address moves to the page's host.
  await page.goto(`${BASE}/a/${a.id}/`);
  await expect(page).toHaveURL(a.url);
  await expect(page.locator("#artifacts-rt")).toBeAttached();

  await selectText(page, "#p2");
  const shadow = page.locator("#artifacts-rt");
  await shadow.locator(".fab").click();
  await shadow.locator(".pop input").fill("Reader");
  await shadow.locator(".pop textarea").fill("Posted from the page host.");
  await shadow.locator(".pop .send").click();
  await expect(shadow.locator(".thread")).toContainText("Posted from the page host.");
  const owner = await api(`/api/artifacts/${a.id}/comments`);
  expect(owner.threads.map((t) => t.comments[0].body)).toContain("Posted from the page host.");

  // The version switcher stays on the page's host.
  publish({ "index.html": html.replace("Quarterly Report", "Annual Report") }, a.id);
  await page.goto(`${a.url}v/1/`);
  await expect(page.locator("#title")).toHaveText("Quarterly Report");
  await shadow.locator(".launcher").click();
  await shadow.locator("select").selectOption("2");
  await expect(page).toHaveURL(a.url);
  await expect(page.locator("#title")).toHaveText("Annual Report");
});

test("one page cannot read or write another, even one the visitor unlocked", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const hostile = publish({ "index.html": "<html><body><p>hostile</p></body></html>" });
  const secret = publish({ "index.html": html.replace("Quarterly Report", "Secret Report") }, undefined, { visibility: "private" });
  const { share_code: code, share_url: shareURL } = await api(`/api/artifacts/${secret.id}/share-code`, {
    method: "POST",
    body: JSON.stringify({ action: "issue" }),
  });
  const secretOrigin = new URL(shareURL).origin;
  expect(shareURL).toBe(`${secretOrigin}/?k=${code}`);

  // The visitor opens the old share link: it unlocks on the page's host.
  await page.goto(`${BASE}/a/${secret.id}/?k=${code}`);
  await expect(page).toHaveURL(`${secretOrigin}/`);
  await expect(page.locator("#title")).toHaveText("Secret Report");

  // Then visits the hostile page, whose script tries everything it can.
  await page.goto(hostile.url);
  const tried = await page.evaluate(
    async ({ origin, id }) => {
      const out = {};
      for (const [name, url] of [["readPage", `${origin}/`], ["readComments", `${origin}/c/${id}/comments?status=all`]]) {
        try {
          out[name] = (await fetch(url, { credentials: "include" })).status;
        } catch {
          out[name] = "blocked";
        }
      }
      out.cookie = document.cookie;
      return out;
    },
    { origin: secretOrigin, id: secret.id }
  );
  expect(tried).toEqual({ readPage: "blocked", readComments: "blocked", cookie: "" });

  // A plain form post is not stopped by the page's CSP, and the hosts are
  // same-site, so the browser attaches the secret page's Lax cookie. Only
  // the Origin check stands between the hostile page and a forged comment.
  const posted = page.waitForResponse((r) => r.request().method() === "POST" && r.url().startsWith(secretOrigin));
  await page.evaluate(
    ({ origin, id }) => {
      // Top-level, since the page's CSP keeps it from framing another host.
      const form = document.createElement("form");
      form.method = "POST";
      form.enctype = "text/plain";
      form.action = `${origin}/c/${id}/comments`;
      const field = document.createElement("input");
      // text/plain sends name=value, which this makes valid JSON.
      field.name = JSON.stringify({ author_name: "x", body: "forged", anchor: { kind: "text", quote: "Revenue" }, version: 1, pad: "" }).slice(0, -2);
      field.value = '"}';
      form.appendChild(field);
      document.body.appendChild(form);
      form.submit();
    },
    { origin: secretOrigin, id: secret.id }
  );
  const res = await posted;
  expect(await res.request().headerValue("cookie")).toContain("artifact_unlock=");
  expect(res.status()).toBe(403);
  const owner = await api(`/api/artifacts/${secret.id}/comments`);
  expect(owner.threads).toHaveLength(0);
});

// A page that keeps state the way the guides teach it: a checkbox whose
// value lives in window.byagent.state, read on load and on every change.
const STATE_PAGE = `<!doctype html><html><head><title>Checklist</title></head><body>
<label><input type="checkbox" id="done"> Ship it</label>
<p id="status">loading</p>
<script type="module">
addEventListener("DOMContentLoaded", async () => {
  const state = window.byagent && window.byagent.state;
  if (!state) { document.querySelector("#status").textContent = "no state"; return; }
  const box = document.querySelector("#done");
  const show = async () => {
    box.checked = (await state.get("done")) === true;
    document.querySelector("#status").textContent = box.checked ? "done" : "open";
  };
  box.addEventListener("change", () => state.set("done", box.checked));
  state.on("change", show);
  await show();
});
</script></body></html>`;

test("page state is shared by every reader of a page and by no other page", async ({ browser }) => {
  const a = publish({ "index.html": STATE_PAGE });
  const b = publish({ "index.html": STATE_PAGE });
  const alice = await (await browser.newContext()).newPage();
  const bob = await (await browser.newContext()).newPage();
  await alice.goto(a.url);
  await bob.goto(a.url);
  await expect(alice.locator("#status")).toHaveText("open");
  await expect(bob.locator("#status")).toHaveText("open");

  await alice.locator("#done").check();
  await expect(alice.locator("#status")).toHaveText("done");
  // Bob's page picks it up on its next check (every 15 s, or on focus).
  await bob.evaluate(() => window.byagent.state.refresh());
  await expect(bob.locator("#status")).toHaveText("done");
  // And a fresh load remembers it.
  await bob.reload();
  await expect(bob.locator("#status")).toHaveText("done");

  // The other page has state of its own.
  await bob.goto(b.url);
  await expect(bob.locator("#status")).toHaveText("open");
  // Its script cannot reach page A's state, by URL or by form post.
  const tried = await bob.evaluate(async (origin) => {
    try {
      return (await fetch(`${origin}/_state`, { credentials: "include" })).status;
    } catch {
      return "blocked";
    }
  }, new URL(a.url).origin);
  expect(tried).toBe("blocked");

  const owner = await api(`/api/artifacts/${a.id}/state`);
  expect(owner.items).toEqual({ done: true });
  expect((await api(`/api/artifacts/${b.id}/state`)).items).toEqual({});
});

test("with comments off the runtime only offers state, and --no-state removes it", async ({ page }) => {
  const quiet = publish({ "index.html": STATE_PAGE }, undefined, { manifest: { comments_enabled: false } });
  await page.goto(quiet.url);
  await expect(page.locator("#status")).toHaveText("open");
  await expect(page.locator("#artifacts-rt")).toHaveCount(0);
  await page.locator("#done").check();
  await expect.poll(async () => (await api(`/api/artifacts/${quiet.id}/state`)).items).toEqual({ done: true });

  publish({ "index.html": STATE_PAGE }, quiet.id, { manifest: { state_enabled: false } });
  await page.reload();
  await expect(page.locator("#status")).toHaveText("no state");
  expect(await page.locator("script[data-artifact]").count()).toBe(0);
});
