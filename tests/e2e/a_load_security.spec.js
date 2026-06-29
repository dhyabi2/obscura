const { test, expect } = require("@playwright/test");
const { gotoWallet, autoDialogs, PASS } = require("./helpers");

test.describe("A. load / integrity / security", () => {
  test("UC1/UC3 wallet loads + wasm initializes (all obx fns)", async ({ page }) => {
    await gotoWallet(page);
    await expect(page).toHaveTitle(/Obscura/i);
    const keys = await page.evaluate(() => Object.keys(window).filter(k => /^obx[A-Z]/.test(k)));
    expect(keys).toEqual(expect.arrayContaining(["obxGenerate","obxRestore","obxBuildSend","obxReleaseReservation","obxScanUndo"]));
  });
  test("UC5/UC6 CSP meta + wasm_exec SRI present", async ({ page }) => {
    await gotoWallet(page);
    const csp = await page.locator('meta[http-equiv="Content-Security-Policy"]').getAttribute("content");
    expect(csp).toContain("wasm-unsafe-eval");
    const sri = await page.locator('script[src*="wasm_exec"]').getAttribute("integrity");
    expect(sri).toMatch(/^sha384-/);
  });
  test("UC8 build-hash footer shown after verified load", async ({ page }) => {
    await gotoWallet(page);
    await expect(page.locator("#buildHash")).toContainText(/[0-9a-f]{16,}/, { timeout: 8000 });
  });
  test("UC10 obxReady does not precede function availability (race)", async ({ page }) => {
    await page.goto("/wallet.html", { waitUntil: "domcontentloaded" });
    // when obxReady flips true, the functions must already exist
    const ok = await page.waitForFunction(() => {
      if (window.obxReady !== true) return false;
      return typeof window.obxGenerate === "function" && typeof window.obxBuildSend === "function";
    }, { timeout: 20000 }).then(() => true).catch(() => false);
    expect(ok, "obxReady true but functions missing = race bug").toBe(true);
  });
});

test.describe("B. creation + at-rest encryption", () => {
  test("UC11/14/15/16 create prompts passphrase, stores ENCRYPTED blob (no plaintext)", async ({ page }) => {
    autoDialogs(page, { passphrase: PASS });
    await gotoWallet(page);
    await page.evaluate(() => localStorage.clear());
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.waitForFunction(() => typeof window.obxGenerate === "function");
    await page.locator('[onclick^="createWallet"]').first().click();
    await page.waitForFunction(() => !!localStorage.getItem("obx_mnemonic_enc"), { timeout: 10000 });
    const st = await page.evaluate(() => ({
      enc: localStorage.getItem("obx_mnemonic_enc"),
      plain: localStorage.getItem("obx_mnemonic"),
    }));
    expect(st.plain, "plaintext seed must NOT be stored").toBeFalsy();
    const blob = JSON.parse(st.enc);
    expect(blob).toHaveProperty("ct"); expect(blob).toHaveProperty("salt"); expect(blob).toHaveProperty("iv");
    expect(blob.iters || (blob.kdf && blob.iters)).toBeGreaterThanOrEqual(200000);
    const seed = await page.locator("#newSeed .seedbox").first().innerText();
    expect(seed.trim().split(/\s+/).length).toBe(24);
  });
  test("UC12 passphrase < 8 chars rejected (retry)", async ({ page }) => {
    let prompts = 0, alerted = false;
    page.on("dialog", async d => {
      if (d.type() === "alert") { alerted = true; return d.accept(); }
      prompts++;
      if (prompts === 1) return d.accept("short");      // too short -> alert + retry
      return d.accept(PASS);                            // then valid (both prompts)
    });
    await gotoWallet(page);
    await page.evaluate(() => localStorage.clear());
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.waitForFunction(() => typeof window.obxGenerate === "function");
    await page.locator('[onclick^="createWallet"]').first().click();
    await page.waitForFunction(() => !!localStorage.getItem("obx_mnemonic_enc"), { timeout: 10000 });
    expect(alerted, "short passphrase should trigger an alert").toBe(true);
  });
});
