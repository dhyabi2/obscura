/*
 * Playwright DOM smoke test for the Obscura dashboard.
 *
 * Requires Playwright. If it is not installed, see docs/UIUX_AUDIT.md for the
 * fallback Go-based structure validator (tests/ui/validate_html.go) and the
 * jsdom interaction test (tests/ui/dom_test.js), which run without a browser.
 *
 * Run:
 *   1. Build & start the dashboard:
 *        go build -o bin/obscura-dashboard ./cmd/obscura-dashboard
 *        ./bin/obscura-dashboard --addr 127.0.0.1:8088 &
 *   2. npx playwright test tests/ui/dom_smoke.spec.js
 *      (set BASE_URL to override, default http://127.0.0.1:8088)
 */
const { test, expect } = require("@playwright/test");

const BASE = process.env.BASE_URL || "http://127.0.0.1:8088";

test("page loads with title and key landmarks", async ({ page }) => {
  await page.goto(BASE);
  await expect(page).toHaveTitle(/Obscura/);
  await expect(page.locator("header.app-header")).toBeVisible();
  await expect(page.locator("#panel-node")).toBeVisible();
  await expect(page.locator("#tab-wallet-btn")).toBeVisible();
});

test("node stat cards are present", async ({ page }) => {
  await page.goto(BASE);
  for (const id of ["#stat-height", "#stat-diff", "#stat-supply", "#stat-mempool"]) {
    await expect(page.locator(id)).toHaveCount(1);
  }
});

test("theme toggle switches data-theme", async ({ page }) => {
  await page.goto(BASE);
  const before = await page.locator("html").getAttribute("data-theme");
  await page.locator("#theme-toggle").click();
  const after = await page.locator("html").getAttribute("data-theme");
  expect(after).not.toBe(before);
  expect(["dark", "light"]).toContain(after);
});

test("wallet tab activates and shows send form", async ({ page }) => {
  await page.goto(BASE);
  await page.locator("#tab-wallet-btn").click();
  await expect(page.locator("#panel-wallet")).toBeVisible();
  await expect(page.locator("#send-form")).toBeVisible();
  await expect(page.locator("#tab-wallet-btn")).toHaveAttribute("aria-selected", "true");
});

test("send-form validation triggers on empty submit", async ({ page }) => {
  await page.goto(BASE);
  await page.locator("#tab-wallet-btn").click();
  await page.locator("#send-submit").click();
  // confirm modal must NOT open with empty fields
  await expect(page.locator("#confirm-modal")).toBeHidden();
  // an error must be shown on the recipient field
  await expect(page.locator("#send-to-err")).toBeVisible();
  await expect(page.locator("#send-to")).toHaveAttribute("aria-invalid", "true");
});

test("invalid hex address is rejected", async ({ page }) => {
  await page.goto(BASE);
  await page.locator("#tab-wallet-btn").click();
  await page.locator("#send-to").fill("zzzz");
  await page.locator("#send-amount").fill("1");
  await page.locator("#send-submit").click();
  await expect(page.locator("#confirm-modal")).toBeHidden();
  await expect(page.locator("#send-to-err")).toBeVisible();
});

test("QR canvas renders for an address (if wallet available)", async ({ page }) => {
  await page.goto(BASE);
  await page.locator("#tab-wallet-btn").click();
  // QR may stay hidden if no wallet exists; assert the canvas element exists.
  await expect(page.locator("#address-qr")).toHaveCount(1);
});
