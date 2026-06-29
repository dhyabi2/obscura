const { test } = require("@playwright/test");
test("debug wasm globals", async ({ page }) => {
  const logs = [];
  page.on("console", m => logs.push(`[${m.type()}] ${m.text()}`));
  page.on("pageerror", e => logs.push("PAGEERR: " + e.message));
  await page.goto("/wallet.html", { waitUntil: "networkidle" });
  await page.waitForTimeout(4000);
  const state = await page.evaluate(() => ({
    obxReady: window.obxReady,
    obxKeys: Object.keys(window).filter(k => /^obx/i.test(k)),
    hasGenerate: typeof window.obxGenerate,
    hasGo: typeof window.Go,
    title: document.title,
    bodyHasTamper: document.body.innerText.includes("tamper") || document.body.innerText.includes("Do NOT"),
  }));
  console.log("STATE:" + JSON.stringify(state, null, 2));
  console.log("LOGS:\n" + logs.join("\n"));
});
