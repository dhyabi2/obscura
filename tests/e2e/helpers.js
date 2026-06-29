const { expect } = require("@playwright/test");
const PASS = "test-pass-123456";

// install a dialog auto-responder. prompts get a value based on message; confirms accept.
function autoDialogs(page, { passphrase = PASS, confirmPhrase = null } = {}) {
  page.on("dialog", async (d) => {
    const msg = d.message().toLowerCase();
    try {
      if (d.type() === "prompt") {
        if (msg.includes("passphrase")) return await d.accept(passphrase);
        return await d.accept(confirmPhrase || passphrase);
      }
      return await d.accept(); // confirm/alert
    } catch (_) {}
  });
}

async function gotoWallet(page) {
  await page.goto("/wallet.html", { waitUntil: "domcontentloaded" });
  await page.waitForFunction(() => typeof window.obxGenerate === "function", { timeout: 20000 });
}

// create a fresh wallet via the UI (handles the two passphrase prompts), returns mnemonic+address.
async function createWallet(page, passphrase = PASS) {
  autoDialogs(page, { passphrase });
  await page.evaluate(() => localStorage.clear());
  await page.reload({ waitUntil: "domcontentloaded" });
  await page.waitForFunction(() => typeof window.obxGenerate === "function");
  await page.locator('button:has-text("Create"), [onclick^="createWallet"]').first().click();
  await page.waitForFunction(() => !!localStorage.getItem("obx_mnemonic_enc"), { timeout: 10000 });
  const mnemonic = (await page.locator("#newSeed .seedbox").first().innerText()).trim();
  await page.evaluate(() => { const o = window.obxInfo && window.obxInfo(); window.__addr = o && o.address; });
  return { mnemonic };
}

module.exports = { PASS, autoDialogs, gotoWallet, createWallet, expect };
