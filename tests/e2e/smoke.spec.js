const { test, expect } = require("@playwright/test");

test("wallet page loads, wasm passes integrity check and initializes (obxReady)", async ({ page }) => {
  const errors = [];
  page.on("console", m => { if (m.type() === "error") errors.push(m.text()); });
  page.on("pageerror", e => errors.push("pageerror: " + e.message));
  await page.goto("/wallet.html", { waitUntil: "domcontentloaded" });
  await expect(page).toHaveTitle(/Obscura/i);
  // obxReady is set by the Go WASM main() AFTER the JS sha384 integrity check passes.
  await page.waitForFunction(() => window.obxReady === true, { timeout: 20000 });
  // surface any CSP / wasm / integrity console errors as part of the smoke signal
  console.log("CONSOLE_ERRORS:" + JSON.stringify(errors));
  expect(typeof (await page.evaluate(() => window.obxGenerate))).toBe("function");
});
