/* app.js — Obscura dashboard client. Vanilla JS, no dependencies, offline. */
(function () {
  "use strict";

  // ---------- tiny DOM helpers ----------
  var $ = function (sel, root) { return (root || document).querySelector(sel); };
  var $$ = function (sel, root) { return Array.prototype.slice.call((root || document).querySelectorAll(sel)); };

  var TICKER = "OBX";
  var DECIMALS = 12;
  var NODE_POLL_MS = 5000;
  var MAX_SAMPLES = 60;

  var prefersReducedMotion = window.matchMedia &&
    window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  // ---------- number / amount formatting ----------
  // Format a decimal-string OBX amount to a fixed 12-decimal, grouped display.
  function formatOBX(value) {
    if (value == null || value === "") return "—";
    var s = String(value).trim();
    var neg = s.charAt(0) === "-";
    if (neg) s = s.slice(1);
    var parts = s.split(".");
    var whole = parts[0] || "0";
    var frac = (parts[1] || "");
    frac = (frac + "000000000000").slice(0, DECIMALS);
    var grouped = whole.replace(/\B(?=(\d{3})+(?!\d))/g, ",");
    return (neg ? "-" : "") + grouped + "." + frac;
  }

  function formatInt(n) {
    if (n == null || n === "" || isNaN(Number(n))) return "—";
    return Number(n).toLocaleString("en-US");
  }

  function truncMiddle(str, head, tail) {
    if (!str) return "";
    if (str.length <= head + tail + 1) return str;
    return str.slice(0, head) + "…" + str.slice(str.length - tail);
  }

  function fmtTimestamp(d) {
    // ISO-ish local time with explicit timezone for clarity.
    try {
      return d.toLocaleString(undefined, {
        hour12: false, year: "numeric", month: "2-digit", day: "2-digit",
        hour: "2-digit", minute: "2-digit", second: "2-digit",
        timeZoneName: "short"
      });
    } catch (e) {
      return d.toISOString();
    }
  }

  // ---------- theme ----------
  var THEME_KEY = "obx-theme";
  function applyTheme(theme) {
    document.documentElement.setAttribute("data-theme", theme);
    var btn = $("#theme-toggle");
    var icon = $(".theme-icon", btn);
    var isDark = theme === "dark";
    btn.setAttribute("aria-pressed", String(!isDark));
    btn.setAttribute("aria-label", isDark ? "Switch to light theme" : "Switch to dark theme");
    if (icon) icon.textContent = isDark ? "☾" : "☀";
    try { localStorage.setItem(THEME_KEY, theme); } catch (e) {}
    // Re-render QR with theme-appropriate colors if address present.
    if (state.address) renderQR(state.address);
  }
  function initTheme() {
    var saved;
    try { saved = localStorage.getItem(THEME_KEY); } catch (e) {}
    if (!saved) {
      saved = (window.matchMedia && window.matchMedia("(prefers-color-scheme: light)").matches)
        ? "light" : "dark";
    }
    applyTheme(saved);
    $("#theme-toggle").addEventListener("click", function () {
      var cur = document.documentElement.getAttribute("data-theme");
      applyTheme(cur === "dark" ? "light" : "dark");
    });
  }

  // ---------- toasts ----------
  function toast(message, kind, opts) {
    opts = opts || {};
    var region = $("#toasts");
    var el = document.createElement("div");
    el.className = "toast toast-" + (kind || "info");
    el.setAttribute("role", kind === "error" ? "alert" : "status");
    var msg = document.createElement("span");
    msg.className = "toast-msg";
    msg.textContent = message;
    var close = document.createElement("button");
    close.className = "toast-close";
    close.type = "button";
    close.setAttribute("aria-label", "Dismiss notification");
    close.textContent = "✕";
    var timer;
    function dismiss() {
      if (timer) clearTimeout(timer);
      el.classList.add("toast-out");
      if (prefersReducedMotion) { el.remove(); return; }
      setTimeout(function () { el.remove(); }, 200);
    }
    close.addEventListener("click", dismiss);
    el.appendChild(msg);
    el.appendChild(close);
    region.appendChild(el);
    var ttl = opts.ttl != null ? opts.ttl : (kind === "error" ? 8000 : 4000);
    if (ttl > 0) timer = setTimeout(dismiss, ttl);
    return dismiss;
  }

  // ---------- fetch helper ----------
  function api(path, options) {
    options = options || {};
    var ctrl = new AbortController();
    var t = setTimeout(function () { ctrl.abort(); }, options.timeout || 20000);
    return fetch(path, {
      method: options.method || "GET",
      headers: options.body ? { "Content-Type": "application/json" } : undefined,
      body: options.body ? JSON.stringify(options.body) : undefined,
      signal: ctrl.signal
    }).then(function (res) {
      clearTimeout(t);
      return res.text().then(function (txt) {
        var data = {};
        try { data = txt ? JSON.parse(txt) : {}; } catch (e) { data = { error: txt }; }
        return { ok: res.ok, status: res.status, data: data };
      });
    }).catch(function (err) {
      clearTimeout(t);
      return { ok: false, status: 0, data: { error: err.name === "AbortError" ? "request timed out" : "network error" } };
    });
  }

  // ---------- spinner / busy ----------
  function setBusy(btn, busy) {
    if (!btn) return;
    var spinner = $(".spinner", btn);
    btn.disabled = busy;
    btn.setAttribute("aria-busy", String(busy));
    if (spinner) spinner.hidden = !busy;
  }

  // ---------- state ----------
  var state = {
    address: null,
    samples: [],          // {h: height, t: Date}
    connection: "unknown" // online | offline | syncing | unknown
  };

  // ---------- connection indicator ----------
  function setConnection(kind, label) {
    state.connection = kind;
    var el = $("#conn-indicator");
    el.className = "conn-indicator conn-" + kind;
    $(".conn-label", el).textContent = label;
  }

  // ================= NODE =================
  function setNodeOffline(offline) {
    $("#node-offline-banner").hidden = !offline;
    if (offline) {
      setConnection("offline", "Node offline");
      $$("#node-cards .stat-value").forEach(function (v) { v.removeAttribute("aria-busy"); });
    }
  }

  function refreshNode(manual) {
    var btn = $("#node-refresh");
    if (manual) setBusy(btn, true);
    return api("/api/node/status", { timeout: 12000 }).then(function (r) {
      if (manual) setBusy(btn, false);
      if (!r.ok || r.data.offline || r.data.error) {
        setNodeOffline(true);
        return;
      }
      setNodeOffline(false);
      var d = r.data;
      renderNodeStatus(d);
      pushSample(d.height);
      $("#node-updated").textContent = "Last updated: " + fmtTimestamp(new Date());
      // After status, fetch a few recent blocks.
      return refreshBlocks(d.height);
    });
  }

  function renderNodeStatus(d) {
    function set(id, val) {
      var el = $(id);
      el.removeAttribute("aria-busy");
      el.textContent = val;
    }
    set("#stat-height", formatInt(d.height));
    set("#stat-diff", formatInt(d.difficulty));
    set("#stat-supply", d.emitted_obx ? formatOBX(d.emitted_obx) : formatInt(d.emitted_atomic));
    set("#stat-pool", formatOBX(atomicToOBX(d.incentive_pool_atomic)));
    set("#stat-anon", formatInt(d.accumulator_size));
    set("#stat-mempool", formatInt(d.mempool_size));
    set("#stat-backend", d.accumulator_backend || "—");

    // sync/online indicator
    var prev = state.samples.length ? state.samples[state.samples.length - 1].h : null;
    var statusEl = $("#stat-status");
    statusEl.removeAttribute("aria-busy");
    if (prev != null && d.height > prev) {
      statusEl.textContent = "Syncing";
      setConnection("syncing", "Syncing");
    } else {
      statusEl.textContent = "Online";
      setConnection("online", "Online");
    }
  }

  function atomicToOBX(atomic) {
    if (atomic == null) return null;
    var s = String(atomic);
    if (s.indexOf(".") >= 0) return s; // already decimal
    var neg = s.charAt(0) === "-"; if (neg) s = s.slice(1);
    while (s.length <= DECIMALS) s = "0" + s;
    var whole = s.slice(0, s.length - DECIMALS);
    var frac = s.slice(s.length - DECIMALS);
    return (neg ? "-" : "") + whole + "." + frac;
  }

  function pushSample(height) {
    var h = Number(height);
    if (isNaN(h)) return;
    var last = state.samples[state.samples.length - 1];
    if (last && last.h === h && state.samples.length > 0) {
      // still record time progression but cap duplicates loosely
    }
    state.samples.push({ h: h, t: new Date() });
    if (state.samples.length > MAX_SAMPLES) state.samples.shift();
    drawChart();
  }

  function drawChart() {
    var canvas = $("#height-chart");
    var empty = $("#chart-empty");
    if (state.samples.length < 2) {
      empty.hidden = false;
      canvas.hidden = true;
      return;
    }
    empty.hidden = true;
    canvas.hidden = false;
    var ctx = canvas.getContext("2d");
    var W = canvas.width, H = canvas.height;
    ctx.clearRect(0, 0, W, H);
    var pad = 8;
    var hs = state.samples.map(function (s) { return s.h; });
    var min = Math.min.apply(null, hs), max = Math.max.apply(null, hs);
    if (max === min) max = min + 1;
    var styles = getComputedStyle(document.documentElement);
    var line = styles.getPropertyValue("--accent").trim() || "#6ea8fe";
    var fill = styles.getPropertyValue("--accent-soft").trim() || "rgba(110,168,254,0.15)";
    function x(i) { return pad + (i / (state.samples.length - 1)) * (W - pad * 2); }
    function y(v) { return H - pad - ((v - min) / (max - min)) * (H - pad * 2); }
    ctx.beginPath();
    state.samples.forEach(function (s, i) {
      var px = x(i), py = y(s.h);
      if (i === 0) ctx.moveTo(px, py); else ctx.lineTo(px, py);
    });
    ctx.strokeStyle = line;
    ctx.lineWidth = 2;
    ctx.lineJoin = "round";
    ctx.stroke();
    ctx.lineTo(x(state.samples.length - 1), H - pad);
    ctx.lineTo(x(0), H - pad);
    ctx.closePath();
    ctx.fillStyle = fill;
    ctx.fill();
    $("#chart-sub").textContent = state.samples.length + " samples · " + min + "→" + max;
  }

  var blocksCache = {}; // height -> sizeBytes
  function refreshBlocks(height) {
    var h = Number(height);
    if (isNaN(h)) return Promise.resolve();
    var wanted = [];
    for (var i = h; i > h - 8 && i >= 0; i--) wanted.push(i);
    return Promise.all(wanted.map(function (bh) {
      if (blocksCache[bh] != null) return Promise.resolve({ h: bh, size: blocksCache[bh] });
      return api("/api/node/block?height=" + bh, { timeout: 8000 }).then(function (r) {
        if (r.ok && r.data && typeof r.data.block === "string") {
          var size = Math.floor(r.data.block.length / 2);
          blocksCache[bh] = size;
          return { h: bh, size: size, block: r.data.block };
        }
        return { h: bh, size: null };
      });
    })).then(function (rows) { renderBlocks(rows); });
  }

  function renderBlocks(rows) {
    var body = $("#blocks-body");
    body.textContent = "";
    if (!rows.length) {
      var er = document.createElement("tr");
      er.className = "empty-row";
      er.innerHTML = '<td colspan="3" class="empty-state">No blocks loaded yet.</td>';
      body.appendChild(er);
      return;
    }
    rows.forEach(function (row) {
      var tr = document.createElement("tr");
      var th = document.createElement("td");
      th.className = "num";
      th.textContent = formatInt(row.h);
      var ts = document.createElement("td");
      ts.className = "num";
      ts.textContent = row.size != null ? formatInt(row.size) : "—";
      var td = document.createElement("td");
      var code = document.createElement("code");
      code.className = "hex-cell";
      var hex = row.block || "";
      code.textContent = hex ? truncMiddle(hex, 10, 8) : "unavailable";
      if (hex) code.title = hex;
      td.appendChild(code);
      tr.appendChild(th); tr.appendChild(ts); tr.appendChild(td);
      body.appendChild(tr);
    });
  }

  // ================= WALLET =================
  function refreshWalletAddress() {
    return api("/api/wallet/address", { timeout: 15000 }).then(function (r) {
      if (!r.ok || r.data.error) {
        if (r.data.wallet_missing) $("#wallet-missing-banner").hidden = false;
        $("#wallet-address").textContent = "unavailable";
        $("#copy-address").disabled = true;
        return;
      }
      $("#wallet-missing-banner").hidden = true;
      state.address = r.data.address;
      var el = $("#wallet-address");
      el.textContent = truncMiddle(r.data.address, 12, 12);
      el.title = r.data.address;
      el.setAttribute("aria-label", "Receiving address " + r.data.address);
      $("#copy-address").disabled = false;
      renderQR(r.data.address);
    });
  }

  function renderQR(text) {
    var canvas = $("#address-qr");
    var empty = $("#qr-empty");
    try {
      var styles = getComputedStyle(document.documentElement);
      var dark = styles.getPropertyValue("--qr-dark").trim() || "#000000";
      var light = styles.getPropertyValue("--qr-light").trim() || "#ffffff";
      window.OBXQR.render(canvas, text, { size: 200, level: "M", dark: dark, light: light, quiet: 3 });
      canvas.hidden = false;
      empty.hidden = true;
    } catch (e) {
      canvas.hidden = true;
      empty.hidden = false;
      empty.textContent = "Could not render QR code.";
    }
  }

  function refreshWalletBalance(manual) {
    var btn = $("#wallet-refresh");
    if (manual) setBusy(btn, true);
    return api("/api/wallet/balance", { timeout: 30000 }).then(function (r) {
      if (manual) setBusy(btn, false);
      var balEl = $("#wallet-balance");
      var outEl = $("#wallet-outputs");
      balEl.removeAttribute("aria-busy");
      if (!r.ok || r.data.error) {
        if (r.data.wallet_missing) $("#wallet-missing-banner").hidden = false;
        balEl.textContent = "—";
        outEl.textContent = r.data.node_offline ? "node offline — balance unavailable" : "balance unavailable";
        return;
      }
      $("#wallet-missing-banner").hidden = true;
      balEl.textContent = formatOBX(r.data.balance);
      var n = r.data.spendable_outputs;
      outEl.textContent = (n != null ? formatInt(n) : "—") + " spendable output" + (n === 1 ? "" : "s");
      $("#wallet-updated").textContent = "Last updated: " + fmtTimestamp(new Date());
    });
  }

  function refreshWallet(manual) {
    return Promise.all([refreshWalletAddress(), refreshWalletBalance(manual)]);
  }

  // ---------- copy to clipboard ----------
  function copyAddress() {
    if (!state.address) return;
    var btn = $("#copy-address");
    var label = $(".btn-label", btn);
    var done = function () {
      var old = label.textContent;
      label.textContent = "Copied!";
      btn.classList.add("copied");
      toast("Address copied to clipboard", "success", { ttl: 2500 });
      setTimeout(function () { label.textContent = old; btn.classList.remove("copied"); }, 1800);
    };
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(state.address).then(done).catch(fallbackCopy);
    } else {
      fallbackCopy();
    }
    function fallbackCopy() {
      try {
        var ta = document.createElement("textarea");
        ta.value = state.address;
        ta.setAttribute("readonly", "");
        ta.style.position = "absolute";
        ta.style.left = "-9999px";
        document.body.appendChild(ta);
        ta.select();
        document.execCommand("copy");
        document.body.removeChild(ta);
        done();
      } catch (e) {
        toast("Could not copy — please copy manually", "error");
      }
    }
  }

  // ---------- send form validation ----------
  function showFieldErr(inputId, errId, msg) {
    var input = $(inputId), err = $(errId);
    if (msg) {
      input.setAttribute("aria-invalid", "true");
      err.textContent = msg;
      err.hidden = false;
    } else {
      input.removeAttribute("aria-invalid");
      err.textContent = "";
      err.hidden = true;
    }
  }

  var HEX_RE = /^[0-9a-fA-F]+$/;
  var AMT_RE = /^[0-9]{1,12}(\.[0-9]{1,12})?$/;

  function validateSend(showErrors) {
    var to = $("#send-to").value.trim();
    var amount = $("#send-amount").value.trim();
    var fee = $("#send-fee").value.trim() || "0.0001";
    var ok = true;

    if (!to) { if (showErrors) showFieldErr("#send-to", "#send-to-err", "Enter a recipient address."); ok = false; }
    else if (!HEX_RE.test(to)) { if (showErrors) showFieldErr("#send-to", "#send-to-err", "Address must be hexadecimal (0-9, a-f)."); ok = false; }
    else if (to.length % 2 !== 0) { if (showErrors) showFieldErr("#send-to", "#send-to-err", "Address has an odd number of hex digits."); ok = false; }
    else if (showErrors) showFieldErr("#send-to", "#send-to-err", null);

    if (!amount) { if (showErrors) showFieldErr("#send-amount", "#send-amount-err", "Enter an amount."); ok = false; }
    else if (!AMT_RE.test(amount)) { if (showErrors) showFieldErr("#send-amount", "#send-amount-err", "Amount must be a number with up to 12 decimals."); ok = false; }
    else if (Number(amount) <= 0) { if (showErrors) showFieldErr("#send-amount", "#send-amount-err", "Amount must be greater than zero."); ok = false; }
    else if (showErrors) showFieldErr("#send-amount", "#send-amount-err", null);

    if (fee && !AMT_RE.test(fee)) { if (showErrors) showFieldErr("#send-fee", "#send-fee-err", "Fee must be a number with up to 12 decimals."); ok = false; }
    else if (showErrors) showFieldErr("#send-fee", "#send-fee-err", null);

    return ok ? { to: to, amount: amount, fee: fee } : null;
  }

  // ---------- confirmation modal ----------
  var lastFocused = null;
  function openConfirm(payload) {
    $("#confirm-to").textContent = truncMiddle(payload.to, 14, 14);
    $("#confirm-to").title = payload.to;
    $("#confirm-amount").textContent = formatOBX(payload.amount);
    $("#confirm-fee").textContent = formatOBX(payload.fee);
    var total = (Number(payload.amount) + Number(payload.fee)).toFixed(DECIMALS);
    $("#confirm-total").textContent = formatOBX(total);
    var modal = $("#confirm-modal");
    lastFocused = document.activeElement;
    modal.hidden = false;
    document.body.classList.add("modal-open");
    var cancelBtn = $("#confirm-cancel");
    cancelBtn.focus();
    trapFocus(modal);
    state.pendingSend = payload;
  }
  function closeConfirm() {
    var modal = $("#confirm-modal");
    modal.hidden = true;
    document.body.classList.remove("modal-open");
    releaseFocusTrap();
    if (lastFocused && lastFocused.focus) lastFocused.focus();
    state.pendingSend = null;
  }

  var focusTrapHandler = null;
  function trapFocus(modal) {
    focusTrapHandler = function (e) {
      if (e.key === "Escape") { closeConfirm(); return; }
      if (e.key !== "Tab") return;
      var focusables = $$('button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])', modal)
        .filter(function (el) { return !el.disabled && el.offsetParent !== null; });
      if (!focusables.length) return;
      var first = focusables[0], last = focusables[focusables.length - 1];
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
    };
    document.addEventListener("keydown", focusTrapHandler, true);
  }
  function releaseFocusTrap() {
    if (focusTrapHandler) document.removeEventListener("keydown", focusTrapHandler, true);
    focusTrapHandler = null;
  }

  function doSend() {
    var payload = state.pendingSend;
    if (!payload) return;
    var btn = $("#confirm-send");
    setBusy(btn, true);
    api("/api/wallet/send", { method: "POST", body: payload, timeout: 60000 }).then(function (r) {
      setBusy(btn, false);
      if (!r.ok || r.data.error) {
        var msg = r.data.error || "Send failed";
        if (r.data.insufficient) msg = "Insufficient funds for this amount + fee.";
        else if (r.data.node_offline) msg = "Node is offline — cannot broadcast.";
        else if (r.data.wallet_missing) msg = "No wallet found.";
        toast(msg, "error");
        return;
      }
      closeConfirm();
      var tx = r.data.txid ? " · txid " + truncMiddle(r.data.txid, 8, 6) : "";
      toast("Sent " + formatOBX(r.data.amount || payload.amount) + " " + TICKER + tx, "success", { ttl: 7000 });
      $("#send-form").reset();
      $("#send-fee").value = "0.0001";
      refreshWalletBalance(false);
    });
  }

  // ================= TABS =================
  function activateTab(which) {
    var isNode = which === "node";
    $("#tab-node-btn").setAttribute("aria-selected", String(isNode));
    $("#tab-node-btn").tabIndex = isNode ? 0 : -1;
    $("#tab-wallet-btn").setAttribute("aria-selected", String(!isNode));
    $("#tab-wallet-btn").tabIndex = isNode ? -1 : 0;
    $("#panel-node").hidden = !isNode;
    $("#panel-wallet").hidden = isNode;
    try { localStorage.setItem("obx-tab", which); } catch (e) {}
    if (!isNode && !state.walletLoaded) {
      state.walletLoaded = true;
      refreshWallet(false);
    }
  }

  function initTabs() {
    $("#tab-node-btn").addEventListener("click", function () { activateTab("node"); });
    $("#tab-wallet-btn").addEventListener("click", function () { activateTab("wallet"); });
    // arrow-key navigation between tabs (WAI-ARIA pattern)
    var tabs = [$("#tab-node-btn"), $("#tab-wallet-btn")];
    tabs.forEach(function (tab, i) {
      tab.addEventListener("keydown", function (e) {
        var idx = i;
        if (e.key === "ArrowRight" || e.key === "ArrowDown") idx = (i + 1) % tabs.length;
        else if (e.key === "ArrowLeft" || e.key === "ArrowUp") idx = (i - 1 + tabs.length) % tabs.length;
        else if (e.key === "Home") idx = 0;
        else if (e.key === "End") idx = tabs.length - 1;
        else return;
        e.preventDefault();
        tabs[idx].focus();
        activateTab(idx === 0 ? "node" : "wallet");
      });
    });
    var saved;
    try { saved = localStorage.getItem("obx-tab"); } catch (e) {}
    activateTab(saved === "wallet" ? "wallet" : "node");
  }

  // ================= init =================
  function init() {
    initTheme();
    initTabs();

    $("#footer-host").textContent = location.host || "this machine";

    $("#node-refresh").addEventListener("click", function () { refreshNode(true); });
    $("#wallet-refresh").addEventListener("click", function () { refreshWallet(true); });
    $("#copy-address").addEventListener("click", copyAddress);

    // live validation
    ["#send-to", "#send-amount", "#send-fee"].forEach(function (id) {
      $(id).addEventListener("input", function () { validateSend(true); });
      $(id).addEventListener("blur", function () { validateSend(true); });
    });

    $("#send-form").addEventListener("submit", function (e) {
      e.preventDefault();
      var payload = validateSend(true);
      if (!payload) {
        toast("Please fix the highlighted fields.", "error", { ttl: 4000 });
        var firstErr = $('[aria-invalid="true"]');
        if (firstErr) firstErr.focus();
        return;
      }
      openConfirm(payload);
    });

    $("#confirm-cancel").addEventListener("click", closeConfirm);
    $("#confirm-send").addEventListener("click", doSend);
    $("#confirm-modal").addEventListener("click", function (e) {
      if (e.target === this) closeConfirm(); // click on overlay
    });

    // initial load + polling for node
    refreshNode(false);
    setInterval(function () {
      // never poll-refresh while user is mid-send (modal open) to avoid jumps
      if (!state.pendingSend) refreshNode(false);
    }, NODE_POLL_MS);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }

  // expose a few helpers for tests
  window.OBX = {
    formatOBX: formatOBX,
    formatInt: formatInt,
    truncMiddle: truncMiddle,
    atomicToOBX: atomicToOBX,
    validateSend: validateSend
  };
})();
