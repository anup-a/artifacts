import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./test/e2e",
  timeout: 30000,
  use: { baseURL: process.env.ARTIFACTS_BASE || "http://127.0.0.1:8099" },
  reporter: [["list"]],
});
