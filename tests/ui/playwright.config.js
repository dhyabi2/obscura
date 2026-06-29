// Playwright config for the Obscura dashboard DOM smoke test.
// Run the dashboard first (./bin/obscura-dashboard), then:
//   npx playwright test --config tests/ui/playwright.config.js
const { defineConfig } = require("@playwright/test");

module.exports = defineConfig({
  testDir: ".",
  testMatch: /dom_smoke\.spec\.js/,
  timeout: 15000,
  use: {
    baseURL: process.env.BASE_URL || "http://127.0.0.1:8088",
    headless: true,
  },
});
