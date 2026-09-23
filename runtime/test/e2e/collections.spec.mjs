// Collections end to end: pages published with one --project stack into a
// deck on the Artifacts page, open into a collection page in course order,
// and can be shared as a public index that hides private pages.
// Set COLLECTIONS_SHOTS=<dir> to also save screenshots for review.
import { test, expect } from "@playwright/test";
import { spawn, execFileSync } from "node:child_process";
import { mkdtempSync, writeFileSync, mkdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const REPO = resolve(process.cwd(), "..");
// dashboard.spec.mjs takes ARTIFACTS_E2E_PORT - 1 and comments.spec.mjs the
// port itself; this suite takes the one below both so all three run in parallel.
const PORT = Number(process.env.ARTIFACTS_E2E_PORT || 8099) - 2;
const BASE = `http://127.0.0.1:${PORT}`;
const EMAIL = "learner@example.test";
const SHOTS = process.env.COLLECTIONS_SHOTS || "";

let server;
let logs = "";

function magicLink() {
  const lines = logs.split("\n").filter((l) => l.includes('"magic_link_url"'));
  if (lines.length === 0) throw new Error("no magic link in server log");
  return JSON.parse(lines.at(-1)).magic_link_url;
}

function publish(key, title, extra = []) {
  const dir = mkdtempSync(join(tmpdir(), "artifacts-coll-"));
  writeFileSync(join(dir, "index.html"), `<!doctype html><title>${title}</title><h1>${title}</h1>`);
  const out = execFileSync("node", ["cli/bin/byagent.js", "publish", dir, "--title", title, "--json", ...extra], {
    cwd: REPO,
    encoding: "utf8",
    env: { ...process.env, ARTIFACTS_TOKEN: key, ARTIFACTS_API: BASE },
  });
  return JSON.parse(out);
}

async function shot(page, name) {
  if (!SHOTS) return;
  await page.evaluate(() => document.fonts.ready);
  await page.screenshot({ path: join(SHOTS, `${name}.png`), fullPage: false, animations: "disabled" });
}

test.beforeAll(async () => {
  const dataDir = mkdtempSync(join(tmpdir(), "artifacts-coll-data-"));
  mkdirSync(join(dataDir, "blobs"), { recursive: true });
  execFileSync("go", ["build", "-o", join(dataDir, "server"), "./cmd/artifacts-server"], { cwd: REPO });
  server = spawn(join(dataDir, "server"), [], {
    env: {
      ...process.env,
      DB: "sqlite",
      STORAGE: "local",
      DATA_DIR: dataDir,
      ADDR: `127.0.0.1:${PORT}`,
      PUBLIC_SHARE_ORIGIN: BASE,
      PUBLIC_API_ORIGIN: BASE,
      KEY_SIGNING_SECRET: "e2e-signing-secret",
      EMAIL_PROVIDER: "log",
      ADMIN_TOKEN: "",
      GITHUB_CLIENT_ID: "",
      GITHUB_CLIENT_SECRET: "",
      LIMIT_PUBLISH_PER_MIN: "1000",
    },
    stdio: ["ignore", "pipe", "pipe"],
  });
  server.stdout.on("data", (c) => (logs += c.toString()));
  server.stderr.on("data", (c) => (logs += c.toString()));
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

test("collections: decks, collection page, course order and public index", async ({ page, browser }) => {
  await page.setViewportSize({ width: 1360, height: 960 });
  await page.goto(`${BASE}/app/login`);
  await page.getByLabel("Email").fill(EMAIL);
  await page.getByRole("button", { name: "Email me a sign-in link" }).click();
  await page.goto(magicLink());
  await expect(page).toHaveURL(/\/app\/artifacts$/);

  await page.goto(`${BASE}/app/keys`);
  await page.getByLabel("Key name").fill("collections e2e");
  await page.getByRole("button", { name: "Create key" }).click();
  const key = await page.locator("#new-key").inputValue();

  const course = "learnpath: Async Rust";
  const titles = ["Async Rust skill map", "async fn, async blocks and .await", "Running futures concurrently with join!", "Remedial: join! is concurrent, not parallel"];
  const published = titles.map((t) => publish(key, t, ["--project", course, "--tag", "lesson"]));
  publish(key, "Ctrip A380 seat map renderer", ["--project", "Hi Travel"]);
  publish(key, "Hotel card with conflict notice", ["--project", "Hi Travel"]);
  publish(key, "Old hotel note", ["--project", "Hi-Travel"]);
  publish(key, "Creatica landing and Paper pitch", ["--project", "Creatica"]);
  const hidden = publish(key, "Private grader notes", ["--project", course, "--private"]);

  // Artifacts page: two decks, one chip, one merge suggestion.
  await page.goto(`${BASE}/app/artifacts`);
  const deck = page.locator(".shelf a.deck", { hasText: course });
  await expect(deck).toBeVisible();
  await expect(deck.locator(".deck-count")).toHaveText("5");
  await expect(deck.locator(".deck-card")).toHaveCount(3);
  // Public covers must load. The private page's cover lives under /app/view,
  // which is only served when the share and app origins differ; this suite
  // runs both on one origin, so that one card is checked by its address only.
  await expect(deck.locator(`img[src*="/app/view/${hidden.id}/"]`)).toHaveCount(1);
  for (const img of await deck.locator('img[src*="/a/"]').all()) {
    await expect.poll(() => img.evaluate((i) => i.complete && i.naturalWidth > 0)).toBe(true);
  }
  await expect(page.locator(".shelf a.deck", { hasText: "Hi Travel" }).locator(".deck-card")).toHaveCount(2);
  await expect(page.locator(".chips a.pchip", { hasText: "Creatica" })).toBeVisible();
  await expect(page.locator(".shelf a.deck", { hasText: "Creatica" })).toHaveCount(0);
  await expect(page.locator(".merge-hint")).toContainText("Hi-Travel");
  await deck.hover();
  await shot(page, "artifacts-shelf");

  // The shelf is one row that never scrolls sideways or cuts a deck.
  const shelfFits = await page.locator(".shelf").evaluate((el) => el.scrollWidth <= el.clientWidth);
  expect(shelfFits).toBe(true);

  // Project chips keep to one line; on a narrow window the rest hide behind
  // "+N more", which expands the row in place.
  const chips = page.locator(".chips[data-collapse]");
  const toggle = chips.locator("[data-chips-toggle]");
  await expect(toggle).toBeHidden();
  // Five chips need about 500px; a phone-width window cannot hold them.
  await page.setViewportSize({ width: 420, height: 960 });
  await expect(toggle).toBeVisible();
  await expect(toggle).toHaveText(/^\+\d+ more$/);
  const oneLine = await chips.evaluate((el) => el.getBoundingClientRect().height);
  expect(oneLine).toBeLessThan(40);
  await toggle.click();
  await expect(toggle).toHaveText("Show less");
  expect(await chips.evaluate((el) => el.getBoundingClientRect().height)).toBeGreaterThan(oneLine);
  await toggle.click();
  await expect(toggle).toHaveText(/more$/);
  await page.setViewportSize({ width: 1360, height: 960 });

  // The deck opens the collection in course order (publish order).
  await deck.click();
  await expect(page).toHaveURL(/\/app\/collections\/learnpath:%20Async%20Rust$/);
  const rows = page.locator(".course-row .course-title");
  await expect(rows).toHaveCount(5);
  await expect(rows.first()).toHaveText(titles[0]);

  // Describe, reorder, share.
  await page.getByText("Edit description").click();
  await page.locator("#collection-desc").fill("An adaptive path through async Rust for a TypeScript developer.");
  await page.getByRole("button", { name: "Save" }).click();
  await expect(page.locator(".collection-desc")).toHaveText(/adaptive path/);
  await page.locator(".course-row").first().getByRole("button", { name: "Move down" }).click();
  await expect(rows.nth(1)).toHaveText(titles[0]);
  await page.getByRole("button", { name: "Share collection" }).click();
  const link = (await page.locator(".collection-share code").textContent()).trim();
  expect(link).toMatch(new RegExp(`^${BASE}/s/[A-Za-z0-9]+/$`));
  await shot(page, "collection-page");

  // A stranger sees the public pages in the saved order, and nothing private.
  const anon = await browser.newContext({ viewport: { width: 1100, height: 900 } });
  const pub = await anon.newPage();
  await pub.goto(link);
  await expect(pub.locator(".public-collection .course-title")).toHaveCount(4);
  await expect(pub.locator(".public-collection .course-title").first()).toHaveText(titles[1]);
  await expect(pub.locator(`a[href*="${hidden.id}"]`)).toHaveCount(0);
  await expect(pub.locator(".sidebar")).toHaveCount(0);
  await shot(pub, "public-index");

  // Merging from the suggestion folds Hi-Travel into Hi Travel.
  await page.goto(`${BASE}/app/artifacts`);
  await page.locator(".merge-hint").getByRole("button").click();
  await expect(page).toHaveURL(/\/app\/collections\/Hi%20Travel$/);
  await expect(page.locator(".course-row")).toHaveCount(3);

  // Stop sharing: the public link is gone.
  await page.goto(`${BASE}/app/collections/${encodeURIComponent(course)}`);
  await page.getByRole("button", { name: "Stop sharing" }).click();
  expect((await pub.goto(link)).status()).toBe(404);
  await anon.close();
  expect(published.length).toBe(4);
});
