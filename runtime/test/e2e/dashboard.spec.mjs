// End-to-end flow from spec section 13: sign in by magic link, create a key,
// publish with the CLI, see the artifact in the dashboard, create an
// integrator workspace, publish for an end user, and open that end user's
// portal in a fresh browser context.
import { test, expect } from "@playwright/test";
import { spawn, execFileSync } from "node:child_process";
import { mkdtempSync, writeFileSync, mkdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const REPO = resolve(process.cwd(), "..");
// comments.spec.mjs owns ARTIFACTS_E2E_PORT (default 8099); this suite takes
// the port below it so the two can run in parallel.
const PORT = Number(process.env.ARTIFACTS_E2E_PORT || 8099) - 1;
const BASE = `http://127.0.0.1:${PORT}`;
const EMAIL = "owner@example.test";

let server;
let logs = "";

/** The last magic link the server logged for EMAIL, read from its JSON log. */
function magicLink() {
  const lines = logs.split("\n").filter((l) => l.includes('"magic_link_url"'));
  if (lines.length === 0) throw new Error("no magic link in server log");
  return JSON.parse(lines.at(-1)).magic_link_url;
}

/** Publish one page through the CLI with the given key; returns the CLI JSON. */
function publishWithCLI(key, title, extra = []) {
  const dir = mkdtempSync(join(tmpdir(), "artifacts-dash-"));
  writeFileSync(join(dir, "index.html"), `<!doctype html><title>${title}</title><h1>${title}</h1>`);
  const out = execFileSync(
    "node",
    ["cli/bin/byagent.js", "publish", dir, "--title", title, "--json", ...extra],
    { cwd: REPO, encoding: "utf8", env: { ...process.env, ARTIFACTS_TOKEN: key, ARTIFACTS_API: BASE } },
  );
  return JSON.parse(out);
}

/** Create a named key on the current workspace and return the plaintext. */
async function createKey(page, name) {
  await page.goto(`${BASE}/app/keys`);
  await page.getByLabel("Key name").fill(name);
  await page.getByRole("button", { name: "Create key" }).click();
  await expect(page.getByText("This is the only time the key is shown.")).toBeVisible();
  const key = await page.locator("#new-key").inputValue();
  expect(key).toMatch(/^ak_[0-9A-Za-z]{43}$/);
  return key;
}

test.beforeAll(async () => {
  const dataDir = mkdtempSync(join(tmpdir(), "artifacts-dash-data-"));
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

test("dashboard: magic link, key, CLI publish, integrator workspace and portal", async ({ page, browser }) => {
  // Signed-out visits land on the login page, which offers email only.
  await page.goto(`${BASE}/app/artifacts`);
  await expect(page).toHaveURL(/\/app\/login$/);
  await expect(page.getByRole("button", { name: "Email me a sign-in link" })).toBeVisible();
  await expect(page.getByText("Continue with GitHub")).toHaveCount(0);

  await page.getByLabel("Email").fill(EMAIL);
  await page.getByRole("button", { name: "Email me a sign-in link" }).click();
  await expect(page.getByText("Check your inbox")).toBeVisible();

  await page.goto(magicLink());
  await expect(page).toHaveURL(/\/app\/artifacts$/);
  await expect(page.getByRole("heading", { name: "Connect your agent" })).toBeVisible();
  await expect(page.locator(".sidebar-foot")).toContainText(EMAIL);

  // A key created in the dashboard publishes through the CLI. A page published
  // today is a bento tile under "Today": cover image, project dot, tag chips.
  const personalKey = await createKey(page, "laptop");
  const first = publishWithCLI(personalKey, "Quarterly report", ["--project", "Hi Travel", "--tag", "report", "--tag", "q3"]);
  expect(first.url).toBe(`${BASE}/a/${first.id}/`);
  expect(first.project).toBe("Hi Travel");
  await page.goto(`${BASE}/app/artifacts`);
  const tile = (title) => page.locator(".tile", { has: page.locator(`a.tile-cover[aria-label="${title}"]`) });
  const row = tile("Quarterly report");
  await expect(page.locator(".day-label").first()).toHaveText("Today");
  await expect(row).toHaveClass(/is-hero/);
  await expect(row.locator(".tile-cover img")).toHaveAttribute("src", `${BASE}/a/${first.id}/_cover.svg`);
  expect(await row.locator(".tile-cover img").evaluate((img) => img.complete && img.naturalWidth > 0)).toBe(true);
  await expect(row.locator(".tile-project")).toHaveText("Hi Travel");
  await expect(row.locator(".tag")).toHaveText(["report", "q3"]);
  await expect(row.locator(".tile-open")).toHaveAttribute("href", first.url);
  await expect(page.locator("th", { hasText: "End user" })).toHaveCount(0);

  // The project chip filters the list server-side; "All" clears it.
  await expect(page.locator(".pchip.is-on")).toHaveText("All");
  await page.locator(".pchip", { hasText: "Hi Travel" }).click();
  await expect(page).toHaveURL(`${BASE}/app/artifacts?project=Hi%20Travel`);
  await expect(page.locator(".pchip.is-on")).toContainText("Hi Travel");
  await expect(page.locator(".day-label").first()).toHaveText("This week");
  await expect(row).toBeVisible();
  await page.locator(".pchip", { hasText: "All" }).click();
  await expect(page).toHaveURL(`${BASE}/app/artifacts`);

  // The tile cover opens the detail as a side peek over the list; the URL is
  // the detail page's, so it is shareable, and Escape closes it again.
  await row.locator(".tile-cover").click();
  const peek = page.locator("aside.peek");
  await expect(peek).toBeVisible();
  await expect(peek.locator(".peek-title")).toHaveText("Quarterly report");
  await expect(peek.locator("#versions")).toBeVisible();
  await expect(peek.locator(".detail-cover img")).toHaveAttribute("src", `${BASE}/a/${first.id}/_cover.svg`);
  await expect(page).toHaveURL(`${BASE}/app/artifacts/${first.id}`);
  await expect(page.locator(".bento")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(peek).toBeHidden();
  await expect(page).toHaveURL(`${BASE}/app/artifacts`);

  // Forms inside the peek re-render it in place: no navigation, the switch
  // flips, the About card saves a new project, and the list behind picks up
  // the "off" badge and the new project when the peek closes.
  await page.evaluate(() => { window.__noReload = true; });
  await row.locator(".tile-cover").click();
  await expect(peek).toBeVisible();
  await expect(peek.locator(".switch")).toHaveAttribute("aria-checked", "true");
  await peek.locator(".switch").click();
  await expect(peek.locator(".switch")).toHaveAttribute("aria-checked", "false");
  await expect(peek.getByText("Comments are off for this artifact")).toBeVisible();
  expect(await page.evaluate(() => window.__noReload)).toBe(true);
  await expect(page).toHaveURL(`${BASE}/app/artifacts/${first.id}`);

  const about = peek.locator("#about");
  await expect(about.getByLabel("Project")).toHaveValue("Hi Travel");
  await expect(about.getByLabel("Tags")).toHaveValue("report, q3");
  await about.getByLabel("Project").fill("Distill");
  await about.getByLabel("Tags").fill("Video, storyboard");
  await about.getByLabel("Cover style").selectOption("rings");
  await about.getByRole("button", { name: "Save" }).click();
  await expect(about.getByLabel("Project")).toHaveValue("Distill");
  await expect(about.getByLabel("Tags")).toHaveValue("video, storyboard");
  await expect(about.getByLabel("Cover style")).toHaveValue("rings");
  await expect(peek.locator(".detail-meta .tile-project")).toHaveText("Distill");
  expect(await page.evaluate(() => window.__noReload)).toBe(true);
  // A bad value comes back as the API's message, still inside the peek.
  await about.getByLabel("Tags").fill("a,b,c,d,e,f,g,h,i,j,k");
  await about.getByRole("button", { name: "Save" }).click();
  await expect(about.locator(".alert")).toHaveText(/tags: at most 10 allowed/);
  await expect(about.getByLabel("Tags")).toHaveValue("a,b,c,d,e,f,g,h,i,j,k");
  await expect(peek).toBeVisible();
  expect(await page.evaluate(() => window.__noReload)).toBe(true);

  await page.goBack();
  await expect(peek).toBeHidden();
  await expect(row.locator(".badge.off")).toBeVisible();
  await expect(row.locator(".tile-project")).toHaveText("Distill");
  await expect(page.locator(".pchip", { hasText: "Distill" })).toBeVisible();
  await expect(page.locator(".pchip", { hasText: "Hi Travel" })).toHaveCount(0);
  expect(await page.evaluate(() => window.__noReload)).toBe(true);

  // An integrator workspace attributes artifacts to end users.
  await page.goto(`${BASE}/app/settings`);
  await page.getByLabel("Workspace name").fill("Acme Inc");
  await page.getByRole("button", { name: "Create" }).click();
  await expect(page).toHaveURL(/\/app\/artifacts$/);
  await expect(page.locator(".switcher .ws-name")).toHaveText("Acme Inc");
  await expect(page.locator(".switcher .ws-kind")).toHaveText("integrator");
  await expect(page.getByRole("link", { name: "End users" })).toBeVisible();

  const acmeKey = await createKey(page, "ci");
  const forCustomer = publishWithCLI(acmeKey, "Customer dashboard", ["--end-user", "customer-7"]);
  expect(forCustomer.end_user).toBe("customer-7");
  const internal = publishWithCLI(acmeKey, "Internal notes");

  await page.goto(`${BASE}/app/artifacts`);
  await expect(tile("Customer dashboard")).toBeVisible();
  await expect(tile("Internal notes")).toBeVisible();
  await expect(tile("Quarterly report")).toHaveCount(0);
  await page.goto(`${BASE}/app/artifacts?end_user=customer-7`);
  await expect(page.getByText("Showing artifacts of end user")).toBeVisible();
  await expect(tile("Customer dashboard")).toBeVisible();
  await expect(tile("Internal notes")).toHaveCount(0);

  await page.goto(`${BASE}/app/end-users`);
  const customerRow = page.locator("tr", { hasText: "customer-7" });
  await expect(customerRow).toBeVisible();
  await customerRow.getByRole("button", { name: "Portal link" }).click();
  await expect(page.getByText("Portal link for customer-7")).toBeVisible();
  const portalURL = await page.locator("#portal-url").inputValue();
  expect(portalURL).toMatch(new RegExp(`^${BASE}/p/`));

  // The portal needs no session and shows only that end user's artifacts.
  const context = await browser.newContext();
  const portal = await context.newPage();
  await portal.goto(portalURL);
  await expect(portal.getByRole("heading", { name: "Artifacts from Acme Inc" })).toBeVisible();
  await expect(portal.locator("tr", { hasText: "Customer dashboard" })).toContainText(forCustomer.id);
  await expect(portal.getByText("Internal notes")).toHaveCount(0);
  await expect(portal.getByText("Quarterly report")).toHaveCount(0);
  await expect(portal.getByText(internal.id)).toHaveCount(0);
  await expect(portal.getByRole("button", { name: "Delete" })).toHaveCount(0);
  await expect(portal.getByText("This link expires")).toBeVisible();

  // Without the token the portal path is not a session.
  await portal.goto(`${BASE}/app/artifacts`);
  await expect(portal).toHaveURL(/\/app\/login$/);
  await context.close();
});
