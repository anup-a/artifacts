// Private pages in a real browser. The owner's viewer at /app/view/{id}/ is
// sandboxed (an opaque origin), so the browser treats the page's own image,
// stylesheet and script requests as cross-site and leaves the SameSite=Lax
// session cookie off them. The page's relative assets must load anyway, and
// must not load for anyone who is not signed in to the owning workspace.
//
// Private viewing needs separate app and share origins, so the API origin is
// 127.0.0.1 and the share origin localhost, both on this one server.
import { test, expect } from "@playwright/test";
import { spawn, execFileSync } from "node:child_process";
import { mkdtempSync, writeFileSync, mkdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const REPO = resolve(process.cwd(), "..");
// comments.spec.mjs owns ARTIFACTS_E2E_PORT (default 8099); dashboard,
// collections and page hosts take the three ports below it, this suite the fourth.
const PORT = Number(process.env.ARTIFACTS_E2E_PORT || 8099) - 4;
const BASE = `http://127.0.0.1:${PORT}`;
const SHARE = `http://localhost:${PORT}`;
const EMAIL = "private-owner@example.test";

// A 3x2 opaque PNG, so naturalWidth tells a decoded image from a broken one.
const PNG = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAMAAAACCAIAAAASFvFNAAAAEklEQVR4nGP4z8DwHwyBDAYGAJJ+C/WEd0WNAAAAAElFTkSuQmCC",
  "base64",
);

let server;
let logs = "";

function magicLink() {
  const lines = logs.split("\n").filter((l) => l.includes('"magic_link_url"'));
  if (lines.length === 0) throw new Error("no magic link in server log");
  return JSON.parse(lines.at(-1)).magic_link_url;
}

/** Publish a directory of files privately through the CLI; returns its JSON. */
function publishPrivate(key, files) {
  const dir = mkdtempSync(join(tmpdir(), "artifacts-private-"));
  for (const [name, body] of Object.entries(files)) {
    mkdirSync(join(dir, name, ".."), { recursive: true });
    writeFileSync(join(dir, name), body);
  }
  const out = execFileSync("node", ["cli/bin/byagent.js", "publish", dir, "--private", "--json"], {
    cwd: REPO,
    encoding: "utf8",
    env: { ...process.env, ARTIFACTS_TOKEN: key, ARTIFACTS_API: BASE },
  });
  return JSON.parse(out);
}

/** Every <img> on the page decoded to a real picture. */
function imagesLoaded(page) {
  return page.evaluate(() =>
    Array.from(document.images).map((img) => ({ src: img.getAttribute("src"), ok: img.complete && img.naturalWidth > 0 })),
  );
}

test.beforeAll(async () => {
  const dataDir = mkdtempSync(join(tmpdir(), "artifacts-private-data-"));
  mkdirSync(join(dataDir, "blobs"), { recursive: true });
  execFileSync("go", ["build", "-o", join(dataDir, "server"), "./cmd/artifacts-server"], { cwd: REPO });
  server = spawn(join(dataDir, "server"), [], {
    env: {
      ...process.env,
      DB: "sqlite",
      STORAGE: "local",
      DATA_DIR: dataDir,
      ADDR: `127.0.0.1:${PORT}`,
      PUBLIC_SHARE_ORIGIN: SHARE,
      PUBLIC_API_ORIGIN: BASE,
      KEY_SIGNING_SECRET: "e2e-signing-secret",
      EMAIL_PROVIDER: "log",
      ADMIN_TOKEN: "",
      GITHUB_CLIENT_ID: "",
      GITHUB_CLIENT_SECRET: "",
    },
    stdio: ["ignore", "pipe", "pipe"],
  });
  server.stdout.on("data", (chunk) => {
    logs += chunk.toString();
  });
  server.stderr.on("data", (chunk) => {
    logs += chunk.toString();
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

test("a private page's relative images, styles and scripts load for its signed-in owner", async ({ page, browser }) => {
  await page.goto(`${BASE}/app/login`);
  await page.getByLabel("Email").fill(EMAIL);
  await page.getByRole("button", { name: "Email me a sign-in link" }).click();
  await expect(page.getByText("Check your inbox")).toBeVisible();
  await page.goto(magicLink());
  await expect(page).toHaveURL(/\/app\/artifacts$/);

  await page.goto(`${BASE}/app/keys`);
  await page.getByLabel("Key name").fill("private-view");
  await page.getByRole("button", { name: "Create key" }).click();
  const key = await page.locator("#new-key").inputValue();

  const site = publishPrivate(key, {
    "index.html":
      '<!doctype html><title>Private site</title><link rel="stylesheet" href="css/site.css">' +
      '<h1 id="h">Private site</h1><img id="cover" src="img/cover.png" alt="cover">' +
      '<script src="js/mark.js"></script><a id="next" href="docs/page.html">next</a>',
    "css/site.css": "#h { color: rgb(1, 2, 3); }",
    "js/mark.js": 'document.documentElement.dataset.ran = "yes";',
    "img/cover.png": PNG,
    "docs/page.html": '<!doctype html><title>Second</title><img id="up" src="../img/cover.png" alt="up">',
  });
  expect(site.url).toBe(`${BASE}/app/view/${site.id}/`);

  await page.goto(site.url);
  await expect(page.locator("#h")).toHaveText("Private site");
  // The document stays sandboxed: an opaque origin, no window into the app.
  expect(await page.evaluate(() => window.origin)).toBe("null");
  await expect.poll(() => imagesLoaded(page)).toEqual([{ src: "img/cover.png", ok: true }]);
  await expect(page.locator("#h")).toHaveCSS("color", "rgb(1, 2, 3)");
  await expect(page.locator("html")).toHaveAttribute("data-ran", "yes");

  // A relative link inside the page keeps working, and so do its assets.
  await page.locator("#next").click();
  await expect(page).toHaveTitle("Second");
  await expect.poll(() => imagesLoaded(page)).toEqual([{ src: "../img/cover.png", ok: true }]);

  // The dashboard's preview frame of the page shows its image too.
  await page.goto(`${BASE}/app/artifacts/${site.id}`);
  const preview = page.locator('iframe[title^="Preview of"]');
  await preview.scrollIntoViewIfNeeded();
  const framed = await (await preview.elementHandle()).contentFrame();
  await expect.poll(async () => (await framed.url()).includes("/~cap/")).toBe(true);
  await expect
    .poll(() => framed.evaluate(() => Array.from(document.images).map((i) => i.complete && i.naturalWidth > 0)))
    .toEqual([true]);

  // A pinned version loads its assets the same way.
  await page.goto(`${BASE}/app/view/${site.id}/v/1/`);
  await expect(page.locator("#h")).toHaveText("Private site");
  await expect.poll(() => imagesLoaded(page)).toEqual([{ src: "img/cover.png", ok: true }]);

  // Markdown published privately keeps its relative images too.
  const notes = publishPrivate(key, {
    "index.md": "# Private notes\n\n![chart](img/chart.png)\n",
    "img/chart.png": PNG,
  });
  await page.goto(notes.url);
  await expect(page.locator("h1")).toContainText("Private notes");
  await expect.poll(() => imagesLoaded(page)).toEqual([{ src: "img/chart.png", ok: true }]);

  // The address the owner ended up on is no key for anyone else: a visitor
  // who is not signed in is refused it, the page and its image alike.
  await page.goto(site.url);
  await expect.poll(() => imagesLoaded(page)).toEqual([{ src: "img/cover.png", ok: true }]);
  const ownerURL = page.url();
  const imageURL = new URL("img/cover.png", ownerURL).href;
  const stranger = await browser.newContext();
  const other = await stranger.newPage();
  const doc = await other.goto(ownerURL);
  expect(doc.status()).toBe(404);
  await expect(other.locator("body")).not.toContainText("Private site");
  // Nor does the image load without a valid capability: not at the plain
  // address, and not with the token tampered.
  const plain = `${BASE}/app/view/${site.id}/img/cover.png`;
  const tampered = imageURL.replace(/~cap\/[^/]+\//, "~cap/0.9999999999.AAAAAAAAAAAAAAAAAAAAAA/");
  expect(tampered).not.toBe(imageURL);
  await other.setContent(`<img id="plain" src="${plain}"><img id="tampered" src="${tampered}">`);
  await other.waitForLoadState("load");
  expect(await imagesLoaded(other)).toEqual([
    { src: plain, ok: false },
    { src: tampered, ok: false },
  ]);
  await stranger.close();
});
