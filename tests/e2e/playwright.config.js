const { defineConfig } = require("@playwright/test");
module.exports = defineConfig({
  testDir: ".",
  testMatch: /.*\.spec\.js/,
  timeout: 30000,
  expect: { timeout: 10000 },
  fullyParallel: false,        // one shared node backend
  workers: 1,
  retries: 0,
  reporter: [["list"], ["json", { outputFile: "results.json" }]],
  use: {
    baseURL: process.env.BASE_URL || "http://127.0.0.1:18099",
    headless: true,
    actionTimeout: 8000,
  },
});
