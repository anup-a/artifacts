import { test, expect } from "@playwright/test";
import { spawn, execFileSync } from "node:child_process";
import { mkdtempSync, writeFileSync, readFileSync, mkdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const REPO = resolve(process.cwd(), "..");
const PORT = 8099;
const BASE = `http://127.0.0.1:${PORT}`;
const TOKEN = "e2e-admin-token";

let server;
let dataDir;

/** Publish a directory of files, returning the new artifact id. */
function publish(files, id) {
  const dir = mkdtempSync(join(tmpdir(), "artifacts-e2e-"));
  for (const [name, body] of Object.entries(files)) {
    writeFileSync(join(dir, name), body);
  }
  const args = ["run", "./scripts/e2e-publish.go", BASE, TOKEN, dir];
  if (id) args.push(id);
  const out = execFileSync("go", args, { cwd: REPO, encoding: "utf8" });
  return JSON.parse(out).id;
}

test.beforeAll(async () => {
  dataDir = mkdtempSync(join(tmpdir(), "artifacts-data-"));
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
      // The suite publishes and comments far faster than a human would; the
      // rate limiter itself is covered by the Go tests.
      LIMIT_PUBLISH_PER_MIN: "1000",
      LIMIT_COMMENTS_PER_MIN: "1000",
    },
    stdio: "ignore",
  });
  for (let i = 0; i < 100; i++) {
    try {
      const res = await fetch(`${BASE}/healthz`);
      if (res.ok) return;
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

test("runtime boots and mounts a shadow-root panel", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });
  await page.goto(`${BASE}/a/${id}/`);

  await expect(page.locator("#artifacts-rt")).toBeAttached();
  const hasShadow = await page.evaluate(
    () => !!document.getElementById("artifacts-rt").shadowRoot
  );
  expect(hasShadow).toBe(true);

  // The host page is untouched.
  await expect(page.locator("#p1")).toHaveText(
    "The quick brown fox jumps over the lazy dog."
  );
});

test("page still renders when the comments API is unreachable", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });
  await page.route("**/c/**", (route) => route.abort());

  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));

  await page.goto(`${BASE}/a/${id}/`);
  await expect(page.locator("#p1")).toBeVisible();
  await expect(page.locator("#artifacts-rt")).toBeAttached();

  const label = await page.evaluate(() =>
    document.getElementById("artifacts-rt").shadowRoot.textContent
  );
  expect(label).toContain("unavailable");
  expect(errors).toEqual([]);
});

test("panel lists an existing thread and shows its highlight", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });

  const res = await fetch(`${BASE}/c/${id}/comments`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      author_name: "Dana",
      body: "Is twelve percent right?",
      anchor: {
        kind: "text",
        quote: "twelve percent",
        prefix: "Revenue grew by ",
        suffix: " this quarter.",
      },
      version: 1,
    }),
  });
  expect(res.status).toBe(201);

  await page.goto(`${BASE}/a/${id}/`);

  const mark = page.locator("mark.artifacts-hl");
  await expect(mark).toHaveCount(1);
  await expect(mark).toHaveText("twelve percent");
  await expect(mark).toHaveAttribute("data-status", "attached");

  const shadow = page.locator("#artifacts-rt");
  await shadow.locator(".launcher").click();
  await expect(shadow.locator(".thread")).toHaveCount(1);
  await expect(shadow.locator(".thread")).toContainText("Is twelve percent right?");
  await expect(shadow.locator(".thread")).toContainText("Dana");
});

test("viewer can reply from the panel", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });
  await fetch(`${BASE}/c/${id}/comments`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      author_name: "Dana",
      body: "First",
      anchor: { kind: "text", quote: "brown fox", prefix: "The quick ", suffix: " jumps" },
      version: 1,
    }),
  });

  await page.goto(`${BASE}/a/${id}/`);
  const shadow = page.locator("#artifacts-rt");
  await shadow.locator(".launcher").click();
  // The inline reply box belongs to the active thread only.
  await expect(shadow.locator(".reply")).toHaveCount(0);
  await shadow.locator(".thread").click();
  await expect(shadow.locator(".reply input")).toHaveAttribute("placeholder", "Reply as Anonymous…");
  await shadow.locator(".reply input").fill("Second");
  await shadow.locator(".reply button").click();

  await expect(shadow.locator(".thread .replies")).toContainText("Second");
});

/** Create a thread through the viewer API and return its id. */
async function createThread(id, body, quote, prefix, suffix) {
  const res = await fetch(`${BASE}/c/${id}/comments`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      author_name: "Dana",
      body,
      anchor: { kind: "text", quote, prefix, suffix },
      version: 1,
    }),
  });
  expect(res.status).toBe(201);
  return (await res.json()).id;
}

test("filter chips switch between open, resolved and all threads", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });
  await createThread(id, "Still open", "brown fox", "The quick ", " jumps");
  const done = await createThread(id, "Already handled", "twelve percent", "Revenue grew by ", " this");
  const res = await fetch(`${BASE}/api/artifacts/${id}/comments/${done}/resolve`, {
    method: "POST",
    headers: { Authorization: `Bearer ${TOKEN}` },
  });
  expect(res.status).toBe(200);

  await page.goto(`${BASE}/a/${id}/`);
  const shadow = page.locator("#artifacts-rt");
  await expect(shadow.locator(".launcher .count")).toHaveText("1");
  await shadow.locator(".launcher").click();

  await expect(shadow.locator("header .cnt")).toHaveText("1 open");
  await expect(shadow.locator(".chip[data-on='1']")).toHaveText("Open");
  await expect(shadow.locator(".thread")).toHaveCount(1);
  await expect(shadow.locator(".thread")).toContainText("Still open");

  await shadow.locator(".chip", { hasText: "Resolved" }).click();
  await expect(shadow.locator(".chip", { hasText: "Resolved" })).toHaveText("Resolved 1");
  await expect(shadow.locator(".thread")).toHaveCount(1);
  await expect(shadow.locator(".thread")).toContainText("Already handled");

  await shadow.locator(".chip", { hasText: "All" }).click();
  await expect(shadow.locator(".thread")).toHaveCount(2);
});

test("escape closes the panel", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });
  await page.goto(`${BASE}/a/${id}/`);
  const shadow = page.locator("#artifacts-rt");
  await shadow.locator(".launcher").click();
  await expect(shadow.locator(".panel")).toHaveAttribute("data-open", "1");
  await page.keyboard.press("Escape");
  await expect(shadow.locator(".panel")).toHaveAttribute("data-open", "0");

  // The close button does the same.
  await shadow.locator(".launcher").click();
  await shadow.locator(".close").click();
  await expect(shadow.locator(".panel")).toHaveAttribute("data-open", "0");
});

test("thread row shows avatar initial, relative time and status badge", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });
  await createThread(id, "Looks fine", "brown fox", "The quick ", " jumps");
  await page.goto(`${BASE}/a/${id}/`);
  const shadow = page.locator("#artifacts-rt");
  await shadow.locator(".launcher").click();
  await expect(shadow.locator(".thread .av")).toHaveText("D");
  await expect(shadow.locator(".thread .name")).toHaveText("Dana");
  await expect(shadow.locator(".thread .time")).toHaveText("· now");
  await expect(shadow.locator(".thread .badge")).toHaveText("attached");
});

/** Select the text of an element by its id, in the page. */
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

test("select text, comment, and see it persist after reload", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });
  await page.goto(`${BASE}/a/${id}/`);

  await selectText(page, "#p2");

  const shadow = page.locator("#artifacts-rt");
  await shadow.locator(".fab").click();
  await shadow.locator(".pop input").fill("Ravi");
  await shadow.locator(".pop textarea").fill("Check this number.");
  await shadow.locator(".pop .send").click();

  await expect(shadow.locator(".thread")).toContainText("Check this number.");

  // Survives a reload, with a highlight and a remembered name.
  await page.reload();
  await expect(page.locator("mark.artifacts-hl")).toHaveCount(1);
  await shadow.locator(".launcher").click();
  await expect(shadow.locator(".thread")).toContainText("Ravi");

  await selectText(page, "#p1");
  await shadow.locator(".fab").click();
  await expect(shadow.locator(".pop input")).toHaveValue("Ravi");
});

test("element pick mode anchors to an element", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });
  await page.goto(`${BASE}/a/${id}/`);

  const shadow = page.locator("#artifacts-rt");
  await shadow.locator(".launcher").click();
  await shadow.locator(".pick").click();
  await expect(shadow.locator(".pick")).toHaveAttribute("data-on", "1");
  await page.locator("#title").click();

  await shadow.locator(".pop input").fill("Sam");
  await shadow.locator(".pop textarea").fill("Rename this heading.");
  await shadow.locator(".pop .send").click();

  await expect(shadow.locator(".thread")).toContainText("Rename this heading.");
  await expect(page.locator("#title[data-artifacts-thread]")).toHaveCount(1);
});

test("republishing changed text drifts the anchor", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });
  await fetch(`${BASE}/c/${id}/comments`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      author_name: "Dana",
      body: "Fox or cat?",
      anchor: {
        kind: "text",
        quote: "brown fox",
        prefix: "The quick ",
        suffix: " jumps over the lazy dog.",
      },
      version: 1,
    }),
  });

  // v2 keeps the surrounding sentence but replaces the quoted words.
  publish({ "index.html": html.replace("brown fox", "brown cat") }, id);

  await page.goto(`${BASE}/a/${id}/`);
  const shadow = page.locator("#artifacts-rt");
  await shadow.locator(".launcher").click();
  await expect(shadow.locator(".badge")).toHaveText("moved");
  await expect(shadow.locator(".badge")).toHaveAttribute("data-s", "drifted");
  await expect(page.locator('mark.artifacts-hl[data-status="drifted"]')).toHaveCount(1);
});

test("replacing the page orphans the anchor and files it under earlier version", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });
  await fetch(`${BASE}/c/${id}/comments`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      author_name: "Dana",
      body: "Gone soon",
      anchor: {
        kind: "text",
        quote: "brown fox",
        prefix: "The quick ",
        suffix: " jumps over the lazy dog.",
      },
      version: 1,
    }),
  });

  publish(
    { "index.html": "<!doctype html><html><body><p>Entirely different copy.</p></body></html>" },
    id
  );

  await page.goto(`${BASE}/a/${id}/`);
  const shadow = page.locator("#artifacts-rt");
  await shadow.locator(".launcher").click();
  await expect(shadow.locator(".section")).toHaveText("On an earlier version");
  await expect(shadow.locator(".badge")).toHaveText("earlier version");
  // The orphaned row sits below the section label, not above it.
  await expect(shadow.locator(".list > :last-child")).toHaveClass(/thread/);
  await expect(page.locator("mark.artifacts-hl")).toHaveCount(0);
});

test("pinned old version is viewable and flagged", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });
  publish({ "index.html": html.replace("Quarterly Report", "Annual Report") }, id);

  await page.goto(`${BASE}/a/${id}/v/1/`);
  await expect(page.locator("#title")).toHaveText("Quarterly Report");

  const shadow = page.locator("#artifacts-rt");
  await shadow.locator(".launcher").click();
  await expect(shadow.locator(".note")).toContainText("Viewing v1");
  await expect(shadow.locator("select")).toBeVisible();
});

test("panel is legible on a dark page and does not leak styles", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-dark.html", "utf8");
  const id = publish({ "index.html": html });
  await page.goto(`${BASE}/a/${id}/`);

  const shadow = page.locator("#artifacts-rt");
  await shadow.locator(".launcher").click();

  const panelBg = await page.evaluate(() => {
    const p = document.getElementById("artifacts-rt").shadowRoot.querySelector(".panel");
    return getComputedStyle(p).backgroundColor;
  });
  expect(panelBg).toBe("rgb(255, 255, 255)");

  // The host page keeps its own colours.
  const bodyColor = await page.evaluate(() => getComputedStyle(document.body).color);
  expect(bodyColor).toBe("rgb(230, 237, 243)");
});

test("panel follows the viewer's colour scheme, not the page's", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });
  await page.emulateMedia({ colorScheme: "dark" });
  await page.goto(`${BASE}/a/${id}/`);

  const shadow = page.locator("#artifacts-rt");
  await shadow.locator(".launcher").click();
  const panelBg = await page.evaluate(() => {
    const p = document.getElementById("artifacts-rt").shadowRoot.querySelector(".panel");
    return getComputedStyle(p).backgroundColor;
  });
  expect(panelBg).toBe("rgb(17, 17, 17)");
});

test("seen beacon reports anchor status back to the owner API", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });
  await fetch(`${BASE}/c/${id}/comments`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      author_name: "Dana",
      body: "status check",
      anchor: { kind: "text", quote: "brown fox", prefix: "The quick ", suffix: " jumps" },
      version: 1,
    }),
  });

  await page.goto(`${BASE}/a/${id}/`);
  await expect(page.locator("mark.artifacts-hl")).toHaveCount(1);

  await expect
    .poll(async () => {
      const res = await fetch(`${BASE}/api/artifacts/${id}/comments`, {
        headers: { Authorization: `Bearer ${TOKEN}` },
      });
      const body = await res.json();
      return body.threads[0].anchor_status;
    })
    .toBe("attached");
});
