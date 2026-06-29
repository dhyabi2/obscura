// UC23-36: backup-confirm gate (C) + restore/address-confirmation (D).
// Tests run against a wallet already served at BASE_URL (no node mgmt here).
//
// Key behaviors verified directly against DOM / localStorage:
//   - #sendBtn disabled until obx_backup_confirmed="1"
//   - confirmBackup() compares re-typed phrase to the in-memory _seed
//   - restoreWallet() validates EXACTLY 12 or 24 words, then shows the derived
//     address (#restoreDerivedAddr) and requires confirmRestore() before opening
//   - confirmRestore() sets obx_backup_confirmed="1"
//   - seed is stored ONLY encrypted (obx_mnemonic_enc), never obx_mnemonic
//
// Wallet quirk worth noting: obxGenerate() ALWAYS yields a 24-word (256-bit) seed;
// there is no UI path to produce a 12-word phrase. For the 12-word restore case we
// build a valid 12-word vector by replicating the wallet's own mnemonic codec (BIP39
// entropy encoding over Obscura's deterministic CVCV wordlist) — proven to decode by
// obxRestore. See VEC12 below.

const { test, expect } = require("@playwright/test");
const { gotoWallet, autoDialogs, PASS } = require("./helpers");

// ---- valid 12- and 13-word vectors (Obscura's own codec, computed once in Node) ----
// 12 words from fixed 16-byte entropy 00112233445566778899aabbccddeeff:
const VEC12 = "baba fose fude foro dore dazo boma dake goti faho fiku kani";
// VEC12 + one extra (still a real word) -> 13 words = invalid count:
const VEC13 = VEC12 + " baba";
// 23 words: a valid 24-word phrase with the last word dropped:
// (built at runtime from obxGenerate so it's a real derived 24 first.)

// create a fresh wallet via the UI; returns the shown 24-word mnemonic.
async function freshCreate(page, passphrase = PASS) {
  autoDialogs(page, { passphrase });
  await gotoWallet(page);
  await page.evaluate(() => localStorage.clear());
  await page.reload({ waitUntil: "domcontentloaded" });
  await page.waitForFunction(() => typeof window.obxGenerate === "function");
  await page.locator('[onclick^="createWallet"]').first().click();
  await page.waitForFunction(() => !!localStorage.getItem("obx_mnemonic_enc"), { timeout: 10000 });
  const mnemonic = (await page.locator("#newSeed .seedbox").first().innerText()).trim();
  // dismiss the "I saved it — continue" so the wallet view is shown
  await page.locator('#newSeed [onclick^="showWallet"]').click().catch(() => {});
  return mnemonic;
}

// open the restore flow with a given phrase (does NOT confirm).
async function typeRestore(page, phrase) {
  await page.fill("#restorePhrase", phrase);
  await page.locator('[onclick^="restoreWallet"]').first().click();
}

// =====================================================================
// C. Backup-confirm gate (UC23-28)
// =====================================================================
test.describe("C. backup-confirm gate (UC23-28)", () => {
  test("UC23 send is disabled until backup is confirmed", async ({ page }) => {
    await freshCreate(page);
    // a freshly created wallet must NOT have the flag set
    const flag = await page.evaluate(() => localStorage.getItem("obx_backup_confirmed"));
    expect(flag, "new wallet must require backup confirmation").not.toBe("1");
    // gate visible, send disabled
    await expect(page.locator("#backupGate")).toBeVisible();
    await expect(page.locator("#sendBtn")).toBeDisabled();
  });

  test("UC24 confirming with the wrong phrase is rejected", async ({ page }) => {
    await freshCreate(page);
    await page.fill("#confirmPhrase", "wrong words that are not the seed at all here now");
    await page.locator('[onclick^="confirmBackup"]').first().click();
    // error shown, flag NOT set, send still disabled
    await expect(page.locator("#backupMsg")).toContainText(/does not match/i);
    const flag = await page.evaluate(() => localStorage.getItem("obx_backup_confirmed"));
    expect(flag).not.toBe("1");
    await expect(page.locator("#sendBtn")).toBeDisabled();
  });

  test("UC25 correct 24 words set obx_backup_confirmed and enable send", async ({ page }) => {
    const mnemonic = await freshCreate(page);
    expect(mnemonic.split(/\s+/).length).toBe(24);
    await page.fill("#confirmPhrase", mnemonic);
    await page.locator('[onclick^="confirmBackup"]').first().click();
    await expect(page.locator("#backupMsg")).toContainText(/confirmed/i);
    await page.waitForFunction(() => localStorage.getItem("obx_backup_confirmed") === "1");
    // backup gate hidden, send enabled (this is the single active tab so owner==true)
    await expect(page.locator("#backupGate")).toBeHidden();
    await expect(page.locator("#sendBtn")).toBeEnabled();
  });

  test("UC26 backup flag persists across reload", async ({ page }) => {
    const mnemonic = await freshCreate(page);
    await page.fill("#confirmPhrase", mnemonic);
    await page.locator('[onclick^="confirmBackup"]').first().click();
    await page.waitForFunction(() => localStorage.getItem("obx_backup_confirmed") === "1");
    // reload: must unlock (passphrase prompt handled by autoDialogs) and keep the flag
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.waitForFunction(() => typeof window.obxGenerate === "function");
    const flag = await page.evaluate(() => localStorage.getItem("obx_backup_confirmed"));
    expect(flag, "flag must survive reload").toBe("1");
    // after unlock the wallet view shows and send is enabled
    await expect(page.locator("#sendBtn")).toBeEnabled();
    await expect(page.locator("#backupGate")).toBeHidden();
  });

  test("UC27 creating a new wallet clears the backup-confirmed flag", async ({ page }) => {
    // confirm backup on the first wallet so the flag is set...
    const mnemonic = await freshCreate(page);
    await page.fill("#confirmPhrase", mnemonic);
    await page.locator('[onclick^="confirmBackup"]').first().click();
    await page.waitForFunction(() => localStorage.getItem("obx_backup_confirmed") === "1");
    // ...then pre-seed the flag and create a brand-new wallet from the noWallet screen.
    // (createWallet() lives behind the noWallet card; we forget+reload to get there, but
    // keep the stale flag to prove createWallet() itself wipes it — not the forget.)
    await page.evaluate(() => {
      localStorage.removeItem("obx_mnemonic_enc");
      localStorage.removeItem("obx_state");
      localStorage.setItem("obx_backup_confirmed", "1"); // stale flag from the old wallet
    });
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.waitForFunction(() => typeof window.obxGenerate === "function");
    // flag is still "1" before create
    expect(await page.evaluate(() => localStorage.getItem("obx_backup_confirmed"))).toBe("1");
    // create another wallet. autoDialogs already set; createWallet prompts twice.
    await page.locator('[onclick^="createWallet"]').first().click();
    await page.waitForFunction(() => !!localStorage.getItem("obx_mnemonic_enc"), { timeout: 10000 });
    const flag = await page.evaluate(() => localStorage.getItem("obx_backup_confirmed"));
    expect(flag, "createWallet() must wipe the stale backup-confirmed flag").not.toBe("1");
  });

  test("UC28 restore sets backup-confirmed (typing the phrase proves possession)", async ({ page }) => {
    // generate a real 24-word seed first, forget, then restore it
    const mnemonic = await freshCreate(page);
    await page.evaluate(() => localStorage.clear());
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.waitForFunction(() => typeof window.obxGenerate === "function");
    await typeRestore(page, mnemonic);
    await expect(page.locator("#restoreDerivedAddr")).toBeVisible();
    await page.locator('#newSeed [onclick^="confirmRestore"]').click();
    await page.waitForFunction(() => localStorage.getItem("obx_backup_confirmed") === "1", { timeout: 10000 });
    const flag = await page.evaluate(() => localStorage.getItem("obx_backup_confirmed"));
    expect(flag).toBe("1");
  });
});

// =====================================================================
// D. Restore + address confirmation (UC29-36)
// =====================================================================
test.describe("D. restore + address confirmation (UC29-36)", () => {
  test("UC29 restore accepts a valid 24-word mnemonic", async ({ page }) => {
    const mnemonic = await freshCreate(page);
    await page.evaluate(() => localStorage.clear());
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.waitForFunction(() => typeof window.obxGenerate === "function");
    await typeRestore(page, mnemonic);
    // derived address shown (no error in #newSeed)
    await expect(page.locator("#restoreDerivedAddr")).toBeVisible();
    const addr = (await page.locator("#restoreDerivedAddr").innerText()).trim();
    expect(addr.length).toBeGreaterThan(0);
    await expect(page.locator("#newSeed")).not.toContainText(/must be 12 or 24/i);
  });

  test("UC30 restore accepts a valid 12-word mnemonic", async ({ page }) => {
    autoDialogs(page, { passphrase: PASS });
    await gotoWallet(page);
    await page.evaluate(() => localStorage.clear());
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.waitForFunction(() => typeof window.obxGenerate === "function");
    // sanity: confirm obxRestore itself decodes our 12-word vector (else our vector is wrong)
    const ok = await page.evaluate((p) => {
      const r = window.obxRestore(p);
      return !r.error;
    }, VEC12);
    expect(ok, "VEC12 must be a valid Obscura 12-word phrase").toBe(true);
    await typeRestore(page, VEC12);
    await expect(page.locator("#restoreDerivedAddr")).toBeVisible();
    await expect(page.locator("#newSeed")).not.toContainText(/must be 12 or 24/i);
  });

  test("UC31 restore rejects an unexpected word count (13 and 23)", async ({ page }) => {
    autoDialogs(page, { passphrase: PASS });
    await gotoWallet(page);
    await page.evaluate(() => localStorage.clear());
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.waitForFunction(() => typeof window.obxGenerate === "function");
    // 13 words
    await typeRestore(page, VEC13);
    await expect(page.locator("#newSeed")).toContainText(/must be 12 or 24/i);
    await expect(page.locator("#restoreDerivedAddr")).toHaveCount(0);
    // 23 words: take a real 24-word phrase, drop one word
    const m24 = await page.evaluate(() => window.obxGenerate().mnemonic);
    const m23 = m24.split(/\s+/).slice(0, 23).join(" ");
    await page.fill("#restorePhrase", m23);
    await page.locator('[onclick^="restoreWallet"]').first().click();
    await expect(page.locator("#newSeed")).toContainText(/must be 12 or 24/i);
    await expect(page.locator("#restoreDerivedAddr")).toHaveCount(0);
  });

  test("UC32 restore shows the derived address and requires 'this is my address' confirm", async ({ page }) => {
    const mnemonic = await freshCreate(page);
    await page.evaluate(() => localStorage.clear());
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.waitForFunction(() => typeof window.obxGenerate === "function");
    await typeRestore(page, mnemonic);
    // address shown + a confirm button present; wallet NOT yet stored/opened
    await expect(page.locator("#restoreDerivedAddr")).toBeVisible();
    await expect(page.locator('#newSeed [onclick^="confirmRestore"]')).toBeVisible();
    const before = await page.evaluate(() => ({
      enc: localStorage.getItem("obx_mnemonic_enc"),
      have: document.getElementById("haveWallet").classList.contains("hide"),
    }));
    expect(before.enc, "wallet must NOT be stored before confirm").toBeFalsy();
    expect(before.have, "wallet view must stay hidden before confirm").toBe(true);
    // now confirm -> wallet opens + stored encrypted (never plaintext)
    await page.locator('#newSeed [onclick^="confirmRestore"]').click();
    await page.waitForFunction(() => !!localStorage.getItem("obx_mnemonic_enc"), { timeout: 10000 });
    const after = await page.evaluate(() => ({
      enc: localStorage.getItem("obx_mnemonic_enc"),
      plain: localStorage.getItem("obx_mnemonic"),
    }));
    expect(after.enc).toBeTruthy();
    expect(after.plain, "must never store plaintext seed").toBeFalsy();
    await expect(page.locator("#haveWallet")).toBeVisible();
  });

  test("UC33 cancelling the address confirm leaves the wallet unstored", async ({ page }) => {
    const mnemonic = await freshCreate(page);
    await page.evaluate(() => localStorage.clear());
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.waitForFunction(() => typeof window.obxGenerate === "function");
    await typeRestore(page, mnemonic);
    await expect(page.locator("#restoreDerivedAddr")).toBeVisible();
    // hit Cancel (the ghost button that clears #newSeed)
    await page.locator('#newSeed button.ghost').click();
    await expect(page.locator("#restoreDerivedAddr")).toHaveCount(0);
    const st = await page.evaluate(() => ({
      enc: localStorage.getItem("obx_mnemonic_enc"),
      flag: localStorage.getItem("obx_backup_confirmed"),
      have: document.getElementById("haveWallet").classList.contains("hide"),
    }));
    expect(st.enc, "cancel must not store the wallet").toBeFalsy();
    expect(st.flag, "cancel must not set backup flag").not.toBe("1");
    expect(st.have, "wallet view must stay hidden after cancel").toBe(true);
    // re-prompt works: re-typing + restoring brings the address back
    await typeRestore(page, mnemonic);
    await expect(page.locator("#restoreDerivedAddr")).toBeVisible();
  });

  test("UC34 12-vs-24 mismatch never silently opens a different wallet", async ({ page }) => {
    autoDialogs(page, { passphrase: PASS });
    await gotoWallet(page);
    await page.evaluate(() => localStorage.clear());
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.waitForFunction(() => typeof window.obxGenerate === "function");
    // paste a 24-word phrase missing one word (23) — a count mismatch.
    const m24 = await page.evaluate(() => window.obxGenerate().mnemonic);
    const m23 = m24.split(/\s+/).slice(0, 23).join(" ");
    await typeRestore(page, m23);
    // it must be REJECTED with a count error, never derive+open a wallet silently
    await expect(page.locator("#newSeed")).toContainText(/must be 12 or 24/i);
    await expect(page.locator("#restoreDerivedAddr")).toHaveCount(0);
    const st = await page.evaluate(() => ({
      enc: localStorage.getItem("obx_mnemonic_enc"),
      have: document.getElementById("haveWallet").classList.contains("hide"),
    }));
    expect(st.enc).toBeFalsy();
    expect(st.have, "no wallet opened on count mismatch").toBe(true);
  });

  test("UC35 whitespace/case in the phrase is normalized", async ({ page }) => {
    const mnemonic = await freshCreate(page);
    // derive the clean address for comparison
    const cleanAddr = await page.evaluate((m) => window.obxRestore(m).address, mnemonic);
    await page.evaluate(() => localStorage.clear());
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.waitForFunction(() => typeof window.obxGenerate === "function");
    // mangle: UPPERCASE, leading/trailing + doubled internal spaces, tabs/newlines
    const mangled = "   " + mnemonic.toUpperCase().split(/\s+/).join("  \t ") + "\n ";
    await typeRestore(page, mangled);
    await expect(page.locator("#restoreDerivedAddr")).toBeVisible();
    const got = (await page.locator("#restoreDerivedAddr").innerText()).trim();
    expect(got, "normalized phrase must derive the same address").toBe(cleanAddr);
  });

  test("UC36 restored wallet derives a deterministic, stable address", async ({ page }) => {
    const mnemonic = await freshCreate(page);
    await page.evaluate(() => localStorage.clear());
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.waitForFunction(() => typeof window.obxGenerate === "function");
    // derive twice via the UI restore flow; addresses must match each other and obxRestore
    await typeRestore(page, mnemonic);
    const a1 = (await page.locator("#restoreDerivedAddr").innerText()).trim();
    await page.locator('#newSeed button.ghost').click(); // cancel
    await typeRestore(page, mnemonic);
    const a2 = (await page.locator("#restoreDerivedAddr").innerText()).trim();
    const a3 = await page.evaluate((m) => window.obxRestore(m).address, mnemonic);
    expect(a1).toBe(a2);
    expect(a1).toBe(a3);
    expect(a1.length).toBeGreaterThan(0);
  });
});
