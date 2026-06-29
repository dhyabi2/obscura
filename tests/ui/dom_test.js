/*
 * jsdom interaction test — runs in Node, no real browser required.
 *
 * Requires: npm install jsdom   (in this directory or globally)
 * Run:      node tests/ui/dom_test.js
 *
 * Validates: HTML structure & ARIA wiring, that app.js loads, the formatting
 * helpers (formatOBX/atomicToOBX/truncMiddle/validateSend), and theme toggle.
 *
 * If jsdom is not installed, this script prints how to install it and exits 0
 * (the Go validator in validate_html.go and the dashboard's main_test.go are
 * the always-available checks).
 */
"use strict";
const fs = require("fs");
const path = require("path");

let JSDOM;
try {
  ({ JSDOM } = require("jsdom"));
} catch (e) {
  console.log("[skip] jsdom not installed. Install with: npm install jsdom");
  console.log("       (Go tests + validate_html.go still cover structure & server.)");
  process.exit(0);
}

const WEBUI = path.join(__dirname, "..", "..", "cmd", "obscura-dashboard", "webui");
const html = fs.readFileSync(path.join(WEBUI, "index.html"), "utf8");
const appJs = fs.readFileSync(path.join(WEBUI, "app.js"), "utf8");
const qrJs = fs.readFileSync(path.join(WEBUI, "qr.js"), "utf8");

let failures = 0;
function check(name, cond) {
  if (cond) console.log("PASS " + name);
  else { console.log("FAIL " + name); failures++; }
}

const dom = new JSDOM(html, {
  runScripts: "outside-only",
  pretendToBeVisual: true,
  url: "http://127.0.0.1:8088/",
});
const { window } = dom;
const { document } = window;

// stub canvas getContext so qr/chart code doesn't throw
window.HTMLCanvasElement.prototype.getContext = function () {
  return {
    fillRect() {}, clearRect() {}, beginPath() {}, moveTo() {}, lineTo() {},
    stroke() {}, fill() {}, closePath() {}, set fillStyle(v) {}, set strokeStyle(v) {},
    set lineWidth(v) {}, set lineJoin(v) {},
  };
};
window.matchMedia = window.matchMedia || function () {
  return { matches: false, addEventListener() {}, removeEventListener() {} };
};
window.fetch = function () {
  return Promise.resolve({ ok: false, status: 0, text: () => Promise.resolve("{}") });
};

// load scripts into the window
window.eval(qrJs);
window.eval(appJs);

// give DOMContentLoaded a tick
const evt = window.document.createEvent("Event");
evt.initEvent("DOMContentLoaded", true, true);
window.document.dispatchEvent(evt);

// ---- structure / aria ----
check("has node tabpanel", !!document.getElementById("panel-node"));
check("has wallet tabpanel", !!document.getElementById("panel-wallet"));
check("tabs have role=tab", document.querySelectorAll('[role="tab"]').length === 2);
check("send form has novalidate", document.getElementById("send-form").hasAttribute("novalidate"));
check("address input no autocomplete", document.getElementById("send-to").getAttribute("autocomplete") === "off");
check("modal is aria-modal", document.getElementById("confirm-modal").getAttribute("aria-modal") === "true");
check("toasts region is aria-live", document.getElementById("toasts").getAttribute("aria-live") === "polite");
check("has skip link", !!document.querySelector(".skip-link"));
check("has noscript fallback", html.indexOf("<noscript>") >= 0);

// ---- helpers exposed by app.js ----
const OBX = window.OBX;
check("OBX helpers exposed", !!OBX);
check("formatOBX pads 12 decimals", OBX.formatOBX("12.5") === "12.500000000000");
check("formatOBX groups thousands", OBX.formatOBX("1234567.1") === "1,234,567.100000000000");
check("atomicToOBX converts", OBX.atomicToOBX("21000000000000") === "21.000000000000");
check("atomicToOBX small", OBX.atomicToOBX("1050000000000") === "1.050000000000");
check("truncMiddle shortens", OBX.truncMiddle("a".repeat(64), 8, 8).indexOf("…") > 0);
check("truncMiddle keeps short", OBX.truncMiddle("abc", 8, 8) === "abc");

// ---- validation ----
document.getElementById("send-to").value = "";
document.getElementById("send-amount").value = "";
check("validateSend rejects empty", OBX.validateSend(false) === null);
document.getElementById("send-to").value = "zzzz";
document.getElementById("send-amount").value = "1";
check("validateSend rejects non-hex", OBX.validateSend(false) === null);
document.getElementById("send-to").value = "aabb";
document.getElementById("send-amount").value = "0";
check("validateSend rejects zero", OBX.validateSend(false) === null);
document.getElementById("send-to").value = "aabbcc";
document.getElementById("send-amount").value = "1.5";
document.getElementById("send-fee").value = "0.0001";
const ok = OBX.validateSend(false);
check("validateSend accepts valid", ok && ok.to === "aabbcc" && ok.amount === "1.5");

// ---- theme toggle ----
const before = document.documentElement.getAttribute("data-theme");
document.getElementById("theme-toggle").dispatchEvent(new window.Event("click"));
const after = document.documentElement.getAttribute("data-theme");
check("theme toggle changes theme", before !== after);

// ---- QR generation ----
try {
  const m = window.OBXQR.generate("a".repeat(128), "M");
  check("QR generates square matrix for address", m.length > 0 && m.length === m[0].length);
} catch (e) {
  check("QR generates square matrix for address", false);
}

console.log(failures === 0 ? "\nALL PASS" : "\n" + failures + " FAILURE(S)");
process.exit(failures === 0 ? 0 : 1);
