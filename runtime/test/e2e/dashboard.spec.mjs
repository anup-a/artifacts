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
const PORT = 8098; // comments.spec.mjs owns 8099; the two suites may run in parallel
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
    ["cli/bin/artifacts.js", "publish", dir, "--title", title, "--json", ...extra],
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
  await expect(page.getByText("Nothing published yet")).toBeVisible();
  await expect(page.locator(".sidebar-foot")).toContainText(EMAIL);

  // A key created in the dashboard publishes through the CLI.
  const personalKey = await createKey(page, "laptop");
  const first = publishWithCLI(personalKey, "Quarterly report");
  expect(first.url).toBe(`${BASE}/a/${first.id}/`);
  await page.goto(`${BASE}/app/artifacts`);
  const row = page.locator("tr", { hasText: "Quarterly report" });
  await expect(row).toContainText(first.id);
  await expect(row.getByRole("link", { name: "Open" })).toHaveAttribute("href", first.url);
  await expect(page.locator("th", { hasText: "End user" })).toHaveCount(0);

  // The row title opens the detail as a side peek over the list; the URL is
  // the detail page's, so it is shareable, and Escape closes it again.
  await row.locator(".row-title").click();
  const peek = page.locator("aside.peek");
  await expect(peek).toBeVisible();
  await expect(peek.locator(".peek-title")).toHaveText("Quarterly report");
  await expect(peek.locator("#versions")).toBeVisible();
  await expect(page).toHaveURL(`${BASE}/app/artifacts/${first.id}`);
  await expect(page.locator(".table-card table.table")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(peek).toBeHidden();
  await expect(page).toHaveURL(`${BASE}/app/artifacts`);

  // Forms inside the peek re-render it in place: no navigation, the switch
  // flips, and the list behind picks up the "off" badge when the peek closes.
  await page.evaluate(() => { window.__noReload = true; });
  await row.getByRole("link", { name: "View" }).click();
  await expect(peek).toBeVisible();
  await expect(peek.locator(".switch")).toHaveAttribute("aria-checked", "true");
  await peek.locator(".switch").click();
  await expect(peek.locator(".switch")).toHaveAttribute("aria-checked", "false");
  await expect(peek.getByText("Comments are off for this artifact")).toBeVisible();
  expect(await page.evaluate(() => window.__noReload)).toBe(true);
  await expect(page).toHaveURL(`${BASE}/app/artifacts/${first.id}`);
  await page.goBack();
  await expect(peek).toBeHidden();
  await expect(row.locator(".badge.off")).toBeVisible();
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
  await expect(page.locator("th", { hasText: "End user" })).toBeVisible();
  await expect(page.locator("tr", { hasText: "Customer dashboard" })).toContainText("customer-7");
  await expect(page.locator("tr", { hasText: "Internal notes" })).toBeVisible();
  await expect(page.locator("tr", { hasText: "Quarterly report" })).toHaveCount(0);

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
