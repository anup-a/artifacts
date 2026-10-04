import { test, expect } from "@playwright/test";
import { spawn, execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdtempSync, writeFileSync, readFileSync, mkdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const REPO = resolve(process.cwd(), "..");
// Override when another server already holds 8099 on the machine.
const PORT = Number(process.env.ARTIFACTS_E2E_PORT || 8099);
const BASE = `http://127.0.0.1:${PORT}`;
const TOKEN = "e2e-admin-token";

let server;
let dataDir;

/** Publish a directory of files, returning the new artifact id. */
function publish(files, id, { visibility } = {}) {
  const dir = mkdtempSync(join(tmpdir(), "artifacts-e2e-"));
  for (const [name, body] of Object.entries(files)) {
    writeFileSync(join(dir, name), body);
  }
  const args = ["run", "./scripts/e2e-publish.go", BASE, TOKEN, dir];
  if (id) args.push(id);
  const env = visibility ? { ...process.env, ARTIFACT_VISIBILITY: visibility } : process.env;
  const out = execFileSync("go", args, { cwd: REPO, encoding: "utf8", env });
  return JSON.parse(out).id;
}

/** Issue a share code for a private artifact, as its owner would. */
async function shareCode(id) {
  const res = await fetch(`${BASE}/api/artifacts/${id}/share-code`, {
    method: "POST",
    headers: { Authorization: `Bearer ${TOKEN}`, "Content-Type": "application/json" },
    body: JSON.stringify({ action: "issue" }),
  });
  if (!res.ok) throw new Error(`share-code: ${res.status}`);
  return (await res.json()).share_code;
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

  // Highlights are hidden until the panel opens, and clicking one does nothing.
  const root = page.locator("html");
  const shadow = page.locator("#artifacts-rt");
  await expect(root).not.toHaveAttribute("data-artifacts-hl", "on");
  await expect(mark).toHaveCSS("background-color", "rgba(0, 0, 0, 0)");
  await mark.click();
  await expect(shadow.locator(".panel")).toHaveAttribute("data-open", "0");

  await shadow.locator(".launcher").click();
  await expect(root).toHaveAttribute("data-artifacts-hl", "on");
  await expect(mark).not.toHaveCSS("background-color", "rgba(0, 0, 0, 0)");
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
  await expect(shadow.locator(".reply textarea")).toHaveAttribute("placeholder", "Reply as Anonymous…");
  // The send button wakes up only once there is something to send.
  await expect(shadow.locator(".reply .send")).toBeDisabled();
  await shadow.locator(".reply textarea").fill("Second");
  await expect(shadow.locator(".reply .send")).toBeEnabled();
  await shadow.locator(".reply .send").click();

  await expect(shadow.locator(".thread .replies .r")).toHaveCount(1);
  await expect(shadow.locator(".thread .replies")).toContainText("Second");
});

test("reply composer: Enter sends, Shift+Enter adds a line", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });
  await createThread(id, "First", "brown fox", "The quick ", " jumps");

  await page.goto(`${BASE}/a/${id}/`);
  const shadow = page.locator("#artifacts-rt");
  await shadow.locator(".launcher").click();
  await shadow.locator(".thread").click();

  const box = shadow.locator(".reply textarea");
  await box.focus();
  await page.keyboard.type("line one");
  await page.keyboard.press("Shift+Enter");
  await page.keyboard.type("line two");
  await expect(box).toHaveValue("line one\nline two");
  await page.keyboard.press("Enter");

  await expect(shadow.locator(".thread .replies")).toContainText("line one");
  await expect(shadow.locator(".thread .replies")).toContainText("line two");
  await expect(shadow.locator(".reply textarea")).toHaveValue("");
});

test("owner replies carry an Author badge in the thread", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });
  const thread = await createThread(id, "Question", "brown fox", "The quick ", " jumps");
  const res = await fetch(`${BASE}/api/artifacts/${id}/comments/${thread}/reply`, {
    method: "POST",
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${TOKEN}` },
    body: JSON.stringify({ body: "Answer" }),
  });
  expect(res.status).toBe(201);

  await page.goto(`${BASE}/a/${id}/`);
  const shadow = page.locator("#artifacts-rt");
  await shadow.locator(".launcher").click();
  await expect(shadow.locator(".thread .more")).toHaveText("1 reply");
  await shadow.locator(".thread").click();
  await expect(shadow.locator(".replies .r .owner")).toHaveText("Author");
  await expect(shadow.locator(".replies .r")).toContainText("Answer");
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

  await expect(shadow.locator("header .cnt")).toHaveText("1");
  await expect(shadow.locator(".chip[aria-selected='true']")).toContainText("Open");
  await expect(shadow.locator(".chip[data-f='open'] .n")).toHaveText("1");
  await expect(shadow.locator(".chip[data-f='resolved'] .n")).toHaveText("1");
  await expect(shadow.locator(".chip[data-f='all'] .n")).toHaveText("2");
  await expect(shadow.locator(".thread")).toHaveCount(1);
  await expect(shadow.locator(".thread")).toContainText("Still open");

  await shadow.locator(".chip[data-f='resolved']").click();
  await expect(shadow.locator(".chip[aria-selected='true']")).toContainText("Resolved");
  await expect(shadow.locator(".thread")).toHaveCount(1);
  await expect(shadow.locator(".thread")).toContainText("Already handled");

  await shadow.locator(".chip[data-f='all']").click();
  await expect(shadow.locator(".thread")).toHaveCount(2);
});

test("launcher hides while the panel is open and shows an empty state", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });
  await page.goto(`${BASE}/a/${id}/`);
  const shadow = page.locator("#artifacts-rt");
  await expect(shadow.locator(".launcher")).toBeVisible();
  await expect(shadow.locator(".launcher .count")).toHaveCount(0);
  await shadow.locator(".launcher").click();
  await expect(shadow.locator(".launcher")).toBeHidden();
  await expect(shadow.locator(".empty h3")).toHaveText("No comments yet");
  // The empty state's pick button is the same mode as the footer one.
  await shadow.locator(".empty .pick").click();
  await expect(shadow.locator(".foot .pick")).toHaveAttribute("data-on", "1");
  await page.keyboard.press("Escape");
  await expect(shadow.locator(".foot .pick")).toHaveAttribute("data-on", "0");
  await expect(shadow.locator(".launcher")).toBeVisible();
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
  await expect(shadow.locator(".thread .time")).toHaveText("now");
  // An attached anchor is the normal case and gets no badge.
  await expect(shadow.locator(".thread .badge")).toHaveCount(0);
  await expect(shadow.locator(".thread .snip")).toHaveText("brown fox");
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
  await expect(shadow.locator(".fab")).toBeVisible();
  await expect(shadow.locator(".fab")).toHaveText("Add comment");
  await shadow.locator(".fab").click();
  await expect(shadow.locator(".fab")).toBeHidden();
  // First visit: no name known yet, so the name field is shown up front.
  await expect(shadow.locator(".pop .snip")).toHaveText("Revenue grew by twelve percent this quarter.");
  await expect(shadow.locator(".pop .who")).toBeHidden();
  await shadow.locator(".pop input").fill("Ravi");
  await shadow.locator(".pop textarea").fill("Check this number.");
  await shadow.locator(".pop .send").click();

  await expect(shadow.locator(".pop")).toBeHidden();
  await expect(shadow.locator(".panel")).toHaveAttribute("data-open", "1");
  await expect(shadow.locator(".thread")).toContainText("Check this number.");

  // Survives a reload, with a highlight and a remembered name.
  await page.reload();
  await expect(page.locator("mark.artifacts-hl")).toHaveCount(1);
  await shadow.locator(".launcher").click();
  await expect(shadow.locator(".thread")).toContainText("Ravi");

  // Second time the name is out of the way, one click away if it needs changing.
  await selectText(page, "#p1");
  await shadow.locator(".fab").click();
  await expect(shadow.locator(".pop input")).toBeHidden();
  await expect(shadow.locator(".pop .who")).toContainText("Commenting as Ravi");
  await shadow.locator(".pop .who .link").click();
  await expect(shadow.locator(".pop input")).toBeVisible();
  await expect(shadow.locator(".pop input")).toHaveValue("Ravi");
});

test("existing highlights survive a comment posted without a reload", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });
  await createThread(id, "First note", "twelve percent", "Revenue grew by ", " this quarter.");
  await page.goto(`${BASE}/a/${id}/`);
  await expect(page.locator("mark.artifacts-hl")).toHaveText("twelve percent");

  // Posting refreshes the thread list in place. The marks already on the page
  // must not eat the anchors of the threads that were painted before it.
  const shadow = page.locator("#artifacts-rt");
  await selectText(page, "#p1");
  await shadow.locator(".fab").click();
  await shadow.locator(".pop input").fill("Ravi");
  await shadow.locator(".pop textarea").fill("Second note.");
  await shadow.locator(".pop .send").click();
  await expect(shadow.locator(".thread")).toHaveCount(2);

  const marks = page.locator("mark.artifacts-hl");
  await expect(marks).toHaveCount(2);
  await expect(marks.nth(0)).toHaveText("The quick brown fox jumps over the lazy dog.");
  await expect(marks.nth(1)).toHaveText("twelve percent");
});

test("compose popover posts with Cmd/Ctrl+Enter, cancels with Escape, and never covers the selection", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });
  await page.goto(`${BASE}/a/${id}/`);
  const shadow = page.locator("#artifacts-rt");

  await selectText(page, "#p2");
  await shadow.locator(".fab").click();
  const popBox = await shadow.locator(".pop").boundingBox();
  const selBox = await page.locator("#p2").boundingBox();
  expect(popBox.y >= selBox.y + selBox.height || popBox.y + popBox.height <= selBox.y).toBe(true);

  // Escape closes the popover without posting.
  await page.keyboard.press("Escape");
  await expect(shadow.locator(".pop")).toBeHidden();

  await selectText(page, "#p2");
  await shadow.locator(".fab").click();
  await shadow.locator(".pop input").fill("Mina");
  await shadow.locator(".pop textarea").focus();
  await page.keyboard.type("Posted by keyboard");
  await page.keyboard.press("ControlOrMeta+Enter");
  await expect(shadow.locator(".pop")).toBeHidden();
  await expect(shadow.locator(".thread")).toContainText("Posted by keyboard");
  await expect(shadow.locator(".thread .name")).toHaveText("Mina");
});

test("element pick mode anchors to an element", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });
  await page.goto(`${BASE}/a/${id}/`);

  const shadow = page.locator("#artifacts-rt");
  await shadow.locator(".launcher").click();
  await shadow.locator(".foot .pick").click();
  await expect(shadow.locator(".foot .pick")).toHaveAttribute("data-on", "1");
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
  await expect(shadow.locator(".badge")).toHaveText("Moved");
  await expect(shadow.locator(".badge")).toHaveAttribute("data-s", "drifted");
  await expect(shadow.locator(".badge")).toHaveAttribute("title", /quoted text changed/);
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
  await expect(shadow.locator(".badge")).toHaveText("Earlier version");
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

/** Republish index.html unchanged with the comments switch set; blobs already exist. */
async function setComments(id, html, enabled) {
  const sha256 = createHash("sha256").update(html).digest("hex");
  const form = new FormData();
  form.append("manifest", JSON.stringify({
    files: [{ path: "index.html", sha256, size: Buffer.byteLength(html) }],
    comments_enabled: enabled,
  }));
  const res = await fetch(`${BASE}/api/artifacts/${id}`, {
    method: "PUT",
    headers: { Authorization: `Bearer ${TOKEN}` },
    body: form,
  });
  expect(res.status).toBe(200);
  expect((await res.json()).comments_enabled).toBe(enabled);
}

test("comments off: page serves without the runtime and the viewer API is gone", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });
  await createThread(id, "Before the switch", "brown fox", "The quick ", " jumps");
  await setComments(id, html, false);

  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto(`${BASE}/a/${id}/`);
  await expect(page.locator("#p1")).toHaveText("The quick brown fox jumps over the lazy dog.");
  await expect(page.locator("#artifacts-rt")).toHaveCount(0);
  await expect(page.locator("mark.artifacts-hl")).toHaveCount(0);
  expect(errors).toEqual([]);

  expect((await fetch(`${BASE}/c/${id}/comments`)).status).toBe(404);
  expect((await fetch(`${BASE}/c/${id}/meta`)).status).toBe(404);
  // The owner still sees the earlier thread.
  const owner = await fetch(`${BASE}/api/artifacts/${id}/comments`, { headers: { Authorization: `Bearer ${TOKEN}` } });
  expect((await owner.json()).threads).toHaveLength(1);

  await setComments(id, html, true);
  // The page is cached for 60s; a reload revalidates against the new ETag.
  await page.goto(`${BASE}/a/${id}/`);
  await page.reload();
  await expect(page.locator("#artifacts-rt")).toBeAttached();
  await expect(page.locator("mark.artifacts-hl")).toHaveCount(1);
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

// A code holder is a viewer, so the comment runtime has to work for them. This
// only fails in a browser: the runtime's own fetch decides whether the unlock
// cookie is sent, and a server-side test sends it either way.
test("an artifact opened with a share code can still be commented on", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html }, undefined, { visibility: "private" });
  const code = await shareCode(id);

  // No code: the gate, not the page.
  await page.goto(`${BASE}/a/${id}/`);
  await expect(page.locator("h1")).toContainText("Enter the code");

  // With the code: the real page, and the runtime attached.
  await page.goto(`${BASE}/a/${id}/?k=${code}`);
  await expect(page).toHaveURL(`${BASE}/a/${id}/`);

  await selectText(page, "#p2");
  const shadow = page.locator("#artifacts-rt");
  await shadow.locator(".fab").click();
  await shadow.locator(".pop input").fill("Friend");
  await shadow.locator(".pop textarea").fill("Opened with the code.");
  await shadow.locator(".pop .send").click();

  await expect(shadow.locator(".thread")).toContainText("Opened with the code.");

  // And it persisted server side, rather than only appearing locally.
  await page.reload();
  await expect(page.locator("mark.artifacts-hl")).toHaveCount(1);
  await shadow.locator(".launcher").click();
  await expect(shadow.locator(".thread")).toContainText("Opened with the code.");
});

test("a reader can suggest an edit, and the panel shows it as a change", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });
  await page.goto(`${BASE}/a/${id}/`);
  const shadow = page.locator("#artifacts-rt");

  await selectText(page, "#p2");
  await shadow.locator(".fab").click();
  await shadow.locator(".pop input").fill("Ravi");
  await shadow.locator(".pop .row .link").click();
  const sug = shadow.locator(".pop textarea.sug");
  await expect(sug).toHaveValue("Revenue grew by twelve percent this quarter.");
  await sug.fill("Revenue grew by 12% this quarter.");
  await shadow.locator(".pop .send").click();

  await expect(shadow.locator(".thread .sug ins")).toHaveText("Revenue grew by 12% this quarter.");
  await expect(shadow.locator(".thread .sug del")).toHaveText("Revenue grew by twelve percent this quarter.");
  const res = await fetch(`${BASE}/c/${id}/comments`);
  const { threads } = await res.json();
  expect(threads[0].suggestion).toBe("Revenue grew by 12% this quarter.");
  expect(threads[0].comments[0].body).toBe("Suggested edit");
});

test("a drifted anchor across list items highlights text without breaking the list", async ({ page }) => {
  const v1 = `<!doctype html><html><head><meta charset="utf-8"><title>Plan</title></head><body>
<h2>Steps</h2>
<ol id="steps"><li id="s1">Switch all traffic on day one.</li><li>Watch errors for two days.</li><li>Remove the old flow.</li></ol>
</body></html>`;
  const id = publish({ "index.html": v1 });
  await page.goto(`${BASE}/a/${id}/`);
  const shadow = page.locator("#artifacts-rt");
  await selectText(page, "#s1");
  await shadow.locator(".fab").click();
  await shadow.locator(".pop input").fill("Priya");
  await shadow.locator(".pop textarea").fill("Pilot it first.");
  await shadow.locator(".pop .send").click();
  await expect(shadow.locator(".thread")).toHaveCount(1);

  // v2 replaces the quoted step with two new ones, so the drifted span
  // covers two <li>s.
  publish(
    { "index.html": v1.replace('<li id="s1">Switch all traffic on day one.</li>', "<li>Pilot with a tenth of traffic.</li><li>Move the rest after a week.</li>") },
    id,
  );
  await page.goto(`${BASE}/a/${id}/`);
  await shadow.locator(".launcher").click();
  await expect(shadow.locator(".badge")).toHaveText("Moved");

  const marks = page.locator('mark.artifacts-hl[data-status="drifted"]');
  await expect(marks).toHaveCount(2);
  await expect(marks.nth(0)).toHaveText("Pilot with a tenth of traffic.");
  await expect(marks.nth(1)).toHaveText("Move the rest after a week.");
  // The list keeps its own items: no empty <li>, no <mark> as a list child.
  await expect(page.locator("#steps > li")).toHaveCount(4);
  await expect(page.locator("#steps > mark")).toHaveCount(0);
  const texts = await page.locator("#steps > li").allTextContents();
  expect(texts.every((t) => t.trim().length > 0)).toBe(true);

  // Closing the panel and switching filters repaints; the list must survive that too.
  await shadow.locator(".chip[data-f='all']").click();
  await expect(page.locator("#steps > li")).toHaveCount(4);
  await expect(page.locator("#steps > mark")).toHaveCount(0);
});

test("a resolved thread drops its Seen/Working badge but keeps Declined", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });
  const owner = (path, body) =>
    fetch(`${BASE}/api/artifacts/${id}/comments/${path}`, {
      method: "POST",
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${TOKEN}` },
      body: JSON.stringify(body || {}),
    });
  const done = await createThread(id, "Fixed it?", "brown fox", "The quick ", " jumps");
  const declined = await createThread(id, "Change it?", "twelve percent", "Revenue grew by ", " this");
  expect((await owner(`${done}/progress`, { progress: "working" })).status).toBe(200);
  expect((await owner(`${done}/resolve`)).status).toBe(200);
  expect((await owner(`${declined}/progress`, { progress: "declined", note: "Out of scope." })).status).toBe(200);

  await page.goto(`${BASE}/a/${id}/`);
  const shadow = page.locator("#artifacts-rt");
  await shadow.locator(".launcher").click();
  await shadow.locator(".chip[data-f='resolved']").click();
  await expect(shadow.locator(".thread")).toHaveCount(2);
  await expect(shadow.locator(".thread", { hasText: "Fixed it?" }).locator(".badge[data-p]")).toHaveCount(0);
  await expect(shadow.locator(".thread", { hasText: "Change it?" }).locator(".badge[data-p='declined']")).toHaveText("Declined");
});

test("compose footer stays on one line, with or without a suggested edit", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });
  await page.goto(`${BASE}/a/${id}/`);
  const shadow = page.locator("#artifacts-rt");
  await selectText(page, "#p2");
  await shadow.locator(".fab").click();
  const row = shadow.locator(".pop .row");
  const send = shadow.locator(".pop .send");
  const oneLine = async () => {
    const r = await row.boundingBox();
    const s = await send.boundingBox();
    // A wrapped row is at least two button heights tall.
    expect(r.height).toBeLessThan(s.height * 1.5);
    for (const sel of [".link", ".hint", ".cancel", ".send"]) {
      const b = await shadow.locator(`.pop .row ${sel}`).boundingBox();
      expect(b.height).toBeLessThan(s.height * 1.5);
    }
  };
  await oneLine();
  await expect(shadow.locator(".pop .row .hint")).toHaveAttribute("title", /to post/);
  await shadow.locator(".pop .row .link").click();
  await expect(shadow.locator(".pop .row .link")).toHaveText("Just comment");
  await oneLine();
});
