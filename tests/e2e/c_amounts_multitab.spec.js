// Use cases 37-54 (amount parsing / send validation + balance formatting, NO-FUNDS parts)
// and 61-65 (multi-tab lock) for the Obscura web wallet.
//
// Funded-only cases (UC45-48, 50, 53, 54) are SKIPPED here: this run has no funds in the
// wallet, so insufficient-balance / successful-broadcast / large-balance-display / post-sync
// balance / coinbase-maturity cases cannot be exercised without mining to the test address.
//
// The amount math (obxToAtomic / atomicToObx) is PURE and needs no funds, so UC37-43, 49,
// 51, 52 are tested directly via page.evaluate. UC44 (malformed send address) needs an
// unlocked, backed-up wallet but no funds — it must reject with a message, not throw.

const { test, expect } = require("@playwright/test");
const { gotoWallet, autoDialogs, createWallet, PASS } = require("./helpers");

// Evaluate window.obxToAtomic(str) in-page; returns {atomic} | {error}.
const toAtomic = (page, s) => page.evaluate((x) => window.obxToAtomic(x), s);
// Evaluate window.atomicToObx(str) in-page; returns human string.
const toObx = (page, a) => page.evaluate((x) => window.atomicToObx(x), a);

// Create a fresh wallet, dismiss the "I saved it" screen to reveal #haveWallet, then confirm
// the backup so the Send box is unlocked. Returns the 24-word mnemonic. (No funds involved.)
async function createAndUnlockSend(page, passphrase = PASS) {
  await createWallet(page, passphrase); // helper stops on the seed-display screen
  const mnemonic = (await page.locator("#newSeed .seedbox").first().innerText()).trim();
  // click "I saved it — continue" to call showWallet() (reveals #haveWallet / #confirmPhrase).
  await page.locator('#newSeed button:has-text("continue")').first().click();
  await expect(page.locator("#confirmPhrase")).toBeVisible({ timeout: 8000 });
  await page.fill("#confirmPhrase", mnemonic);
  await page.locator('button:has-text("Confirm backup")').first().click();
  await page.waitForFunction(
    () => localStorage.getItem("obx_backup_confirmed") === "1",
    { timeout: 8000 }
  );
  return mnemonic;
}

test.describe("E. amount parsing / send validation — BigInt (37-44)", () => {
  test("UC37 amount \"1\" -> 1e12 atomic units", async ({ page }) => {
    await gotoWallet(page);
    const r = await toAtomic(page, "1");
    expect(r.error).toBeFalsy();
    expect(r.atomic).toBe("1000000000000");
  });

  test("UC38 sub-micro \"0.000000000001\" -> 1 atom (not rounded to zero)", async ({ page }) => {
    await gotoWallet(page);
    const r = await toAtomic(page, "0.000000000001");
    expect(r.error).toBeFalsy();
    expect(r.atomic).toBe("1");
  });

  test("UC39 amount with >12 decimals is rejected (no silent truncation)", async ({ page }) => {
    await gotoWallet(page);
    const r = await toAtomic(page, "0.0000000000001"); // 13 decimals
    expect(r.atomic).toBeUndefined();
    expect(r.error).toBeTruthy();
    expect(r.error).toMatch(/decimal/i);
  });

  test("UC40 large amount keeps full precision (no float53 collapse)", async ({ page }) => {
    await gotoWallet(page);
    const input = "90000.000000000001";
    const r = await toAtomic(page, input);
    expect(r.error).toBeFalsy();
    // 90000 * 1e12 + 1 = 90000000000000001 (would be ...000 after Number()/1e12 collapse)
    expect(r.atomic).toBe("90000000000000001");
    // exact round-trip back to the original human string
    const back = await toObx(page, r.atomic);
    expect(back).toBe(input);
    // sanity: prove the float path WOULD have collapsed (documents why BigInt is required)
    const floatCollapsed = await page.evaluate(() => String(Number("90000.000000000001") * 1e12));
    expect(floatCollapsed).not.toBe("90000000000000001");
  });

  test("UC41 zero / negative / non-numeric amount rejected with a clear message", async ({ page }) => {
    await gotoWallet(page);
    const zero = await toAtomic(page, "0");
    expect(zero.atomic).toBeUndefined();
    expect(zero.error).toBeTruthy();

    const neg = await toAtomic(page, "-5");
    expect(neg.atomic).toBeUndefined();
    expect(neg.error).toBeTruthy();

    const abc = await toAtomic(page, "abc");
    expect(abc.atomic).toBeUndefined();
    expect(abc.error).toBeTruthy();

    const empty = await toAtomic(page, "");
    expect(empty.atomic).toBeUndefined();
    expect(empty.error).toBeTruthy();

    const dot = await toAtomic(page, ".");
    expect(dot.atomic).toBeUndefined();
    expect(dot.error).toBeTruthy();
  });

  test("UC42 send blocked when amount resolves to < 1 atom", async ({ page }) => {
    await gotoWallet(page);
    // "0.0000000000004" is 13 decimals -> rejected by the decimal guard; and "0" -> < 1 atom.
    // The exact "rounds below 1 atom" path: a value whose 12-dp truncation is 0 but is non-empty.
    const r = await toAtomic(page, "0.000000000000"); // exactly zero at 12dp
    expect(r.atomic).toBeUndefined();
    expect(r.error).toBeTruthy();
    expect(r.error).toMatch(/(small|atom|valid)/i);
  });

  test("UC43 fee field uses the same BigInt parser", async ({ page }) => {
    await gotoWallet(page);
    // The default fee 0.001 must parse exactly; and a bad fee must be rejected the same way.
    const good = await toAtomic(page, "0.001");
    expect(good.error).toBeFalsy();
    expect(good.atomic).toBe("1000000000"); // 0.001 * 1e12
    const bad = await toAtomic(page, "0.0000000000005"); // 13 decimals
    expect(bad.atomic).toBeUndefined();
    expect(bad.error).toBeTruthy();
  });

  test("UC44 send to a malformed address is rejected (message, not a thrown exception)", async ({ page }) => {
    autoDialogs(page, { passphrase: PASS });
    await gotoWallet(page);
    await createAndUnlockSend(page, PASS);
    // Send button should now be enabled (single tab => owner, backup confirmed).
    await expect(page.locator("#sendBtn")).toBeEnabled();

    // Track any uncaught page error so a thrown exception fails the test rather than passing.
    const pageErrors = [];
    page.on("pageerror", (e) => pageErrors.push(e.message));

    await page.fill("#sendDest", "garbage-not-an-address");
    await page.fill("#sendAmt", "1");
    await page.fill("#sendFee", "0.001");
    await page.locator("#sendBtn").click();

    // wait for the FINAL rejection (the transient "building…" muted message clears first).
    // obxBuildSend returns {error:"bad address: ..."}, surfaced verbatim in #sendMsg.
    await expect(page.locator("#sendMsg")).toContainText(/address/i, { timeout: 8000 });
    const sendMsg = (await page.locator("#sendMsg").innerText()).trim();
    expect(sendMsg.length, "send to garbage address must surface a message").toBeGreaterThan(0);
    // The message must indicate failure, not a success/broadcast.
    expect(sendMsg).not.toMatch(/Broadcast/i);
    expect(pageErrors, "malformed address must be a handled rejection, not a thrown exception").toEqual([]);
  });

  // --- funded-only: SKIPPED (no funds in this run) ---
  test.skip("UC45 send with insufficient balance is rejected (FUNDED)", async () => {
    // SKIP: requires a funded wallet (mine to test address) to trigger insufficient-funds.
  });
  test.skip("UC46 successful send returns a txid and shows Broadcast (FUNDED)", async () => {
    // SKIP: requires spendable funds to actually build + broadcast a tx.
  });
  test.skip("UC47 broadcast failure releases the input reservation (FUNDED)", async () => {
    // SKIP: requires funded inputs to reserve, then a forced broadcast failure.
  });
  test.skip("UC48 reserved-then-released outputs are spendable again (FUNDED)", async () => {
    // SKIP: requires funded outputs to reserve/release.
  });
});

test.describe("F. balance display — BigInt formatting (49-54)", () => {
  test("UC49 balance formats from BigInt, not Number()/1e12 (no float drift)", async ({ page }) => {
    await gotoWallet(page);
    // round-trip several atomic values exactly through atomicToObx -> obxToAtomic.
    const cases = ["1", "1000000000000", "90000000000000001", "9007199254740993"];
    for (const a of cases) {
      const human = await toObx(page, a);
      const r = await toAtomic(page, human);
      expect(r.error, `obxToAtomic(${human}) errored`).toBeFalsy();
      expect(r.atomic, `round-trip drift for atomic ${a}`).toBe(a);
    }
  });

  test("UC51 12-decimal fractional balance trims trailing zeros correctly", async ({ page }) => {
    await gotoWallet(page);
    // 1.500000000000 OBX = 1500000000000 atoms -> displays "1.5", not "1.500000000000".
    expect(await toObx(page, "1500000000000")).toBe("1.5");
    // 2.000000000000 -> "2" (all fractional zeros trimmed, no trailing dot).
    expect(await toObx(page, "2000000000000")).toBe("2");
    // a value with a meaningful low-order digit keeps it.
    expect(await toObx(page, "1000000000001")).toBe("1.000000000001");
    // leading fractional zeros are preserved (not stripped from the front).
    expect(await toObx(page, "1000000000010")).toBe("1.00000000001");
  });

  test("UC52 zero balance shows 0, not NaN/undefined", async ({ page }) => {
    await gotoWallet(page);
    expect(await toObx(page, "0")).toBe("0");
    const z = await toObx(page, "0");
    expect(z).not.toMatch(/NaN|undefined/);
  });

  // --- funded-only: SKIPPED (no funds in this run) ---
  test.skip("UC50 large balance (>9000 OBX) displays exactly (FUNDED)", async () => {
    // SKIP: requires an actual >9000 OBX on-chain balance; pure formatting is covered by UC49.
  });
  test.skip("UC53 balance updates after sync to the funded height (FUNDED)", async () => {
    // SKIP: requires funds mined to the wallet and a synced height change.
  });
  test.skip("UC54 coinbase-maturity-locked vs spendable balance distinguished (FUNDED)", async () => {
    // SKIP: requires coinbase outputs at varying maturity depths.
  });
});

test.describe("H. multi-tab lock (61-65)", () => {
  test("UC61 a single tab becomes the active owner", async ({ page }) => {
    await gotoWallet(page);
    await page.waitForFunction(() => typeof window.tabIsOwner === "function");
    // give the heartbeat a beat to claim the slot.
    await expect.poll(() => page.evaluate(() => window.tabIsOwner())).toBe(true);
    const slot = await page.evaluate(() =>
      JSON.parse(localStorage.getItem("obx_active_tab") || "null")
    );
    expect(slot, "single tab must hold the active_tab slot").toBeTruthy();
    expect(slot.id).toBeTruthy();
  });

  test("UC62 a second tab in the same context sees non-owner + Send disabled", async ({ context }) => {
    const p1 = await context.newPage();
    autoDialogs(p1, { passphrase: PASS });
    await gotoWallet(p1);
    // make a wallet + confirm backup so Send would be enabled if it were owner.
    await createAndUnlockSend(p1, PASS);
    await expect.poll(() => p1.evaluate(() => window.tabIsOwner())).toBe(true);
    await expect(p1.locator("#sendBtn")).toBeEnabled();

    // open a SECOND tab in the SAME context (shares localStorage + BroadcastChannel).
    const p2 = await context.newPage();
    autoDialogs(p2, { passphrase: PASS });
    await gotoWallet(p2);
    await p2.waitForFunction(() => typeof window.tabIsOwner === "function");
    // p2 must yield ownership to the live p1 (via its own beat or a storage/BC event).
    await expect.poll(() => p2.evaluate(() => window.tabIsOwner()), { timeout: 12000 }).toBe(false);
    // backup flag is shared, so the gate would enable Send if it were owner — but it's not.
    await expect(p2.locator("#sendBtn")).toBeDisabled();
    await expect(p2.locator("#sendMsg")).toContainText(/another tab/i, { timeout: 8000 });

    await p1.close();
    await p2.close();
  });

  test("UC63 closing the owner tab lets the other take over within the stale timeout", async ({ context }) => {
    const p1 = await context.newPage();
    autoDialogs(p1, { passphrase: PASS });
    await gotoWallet(p1);
    await createAndUnlockSend(p1, PASS);
    await expect.poll(() => p1.evaluate(() => window.tabIsOwner())).toBe(true);

    const p2 = await context.newPage();
    autoDialogs(p2, { passphrase: PASS });
    await gotoWallet(p2);
    await p2.waitForFunction(() => typeof window.tabIsOwner === "function");
    await expect.poll(() => p2.evaluate(() => window.tabIsOwner()), { timeout: 12000 }).toBe(false);
    await expect(p2.locator("#sendBtn")).toBeDisabled();

    // close the owner — beforeunload releases the slot; p2 reclaims within TAB_STALE (6s) at most.
    await p1.close();
    await expect
      .poll(() => p2.evaluate(() => window.tabIsOwner()), { timeout: 12000 })
      .toBe(true);
    await expect(p2.locator("#sendBtn")).toBeEnabled();
    await expect(p2.locator("#sendMsg")).not.toContainText(/another tab/i);

    await p2.close();
  });

  test("UC64 no false lockout on a single tab (heartbeat survives, stays owner)", async ({ page }) => {
    await gotoWallet(page);
    await page.waitForFunction(() => typeof window.tabIsOwner === "function");
    await expect.poll(() => page.evaluate(() => window.tabIsOwner())).toBe(true);
    // simulate transient focus loss/regain; ownership must persist across several heartbeats.
    await page.evaluate(() => window.dispatchEvent(new Event("blur")));
    await page.waitForTimeout(2500); // > one TAB_BEAT (2000ms)
    await page.evaluate(() => window.dispatchEvent(new Event("focus")));
    await page.waitForTimeout(2500);
    expect(await page.evaluate(() => window.tabIsOwner())).toBe(true);
    await expect(page.locator("#sendBtn")).not.toHaveAttribute("title", /another tab/i);
  });

  test("UC65 a storage/BroadcastChannel event re-evaluates ownership", async ({ context }) => {
    const p1 = await context.newPage();
    autoDialogs(p1, { passphrase: PASS });
    await gotoWallet(p1);
    await p1.waitForFunction(() => typeof window.tabIsOwner === "function");
    await expect.poll(() => p1.evaluate(() => window.tabIsOwner())).toBe(true);

    // a SECOND page writes a fresh foreign slot; the storage event on p1 must demote it
    // (this is the storage-event re-evaluation path, independent of p1's own 2s timer).
    const p2 = await context.newPage();
    await gotoWallet(p2);
    await p2.evaluate(() => {
      const foreign = JSON.stringify({ id: "foreign-tab-xyz", ts: Date.now() });
      localStorage.setItem("obx_active_tab", foreign);
    });
    // p1 should observe the storage event and demote itself promptly (well under TAB_BEAT).
    await expect
      .poll(() => p1.evaluate(() => window.tabIsOwner()), { timeout: 5000 })
      .toBe(false);

    await p1.close();
    await p2.close();
  });
});
