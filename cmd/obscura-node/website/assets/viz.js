/* Obscura — advanced homepage visualizations (vanilla canvas, no deps).
   Covers: strength-vs-other-coins (radar + bars + growth) and the
   "trace the money" interactive. */
(function () {
  "use strict";
  const DPR = () => Math.min(window.devicePixelRatio || 1, 2);

  function fit(canvas) {
    const r = canvas.getBoundingClientRect();
    const d = DPR();
    canvas.width = Math.max(1, r.width * d);
    canvas.height = Math.max(1, r.height * d);
    return { w: canvas.width, h: canvas.height, d };
  }
  const ease = (t) => 1 - Math.pow(1 - t, 3);

  /* ---- coin data (illustrative, higher = stronger) ---- */
  const COINS = {
    obscura: { name: "Obscura", color: "#00e6c3", s: { set: 100, growth: 100, leak: 100, proof: 95, setup: 100, amount: 92 } },
    monero:  { name: "Monero",  color: "#ff7ad9", s: { set: 35,  growth: 15,  leak: 40,  proof: 45, setup: 100, amount: 92 } },
    zcash:   { name: "Zcash",   color: "#f4b728", s: { set: 78,  growth: 68,  leak: 88,  proof: 88, setup: 58,  amount: 92 } },
    bitcoin: { name: "Bitcoin", color: "#9aa0b5", s: { set: 3,   growth: 0,   leak: 4,   proof: 100, setup: 100, amount: 0 } },
    dash:    { name: "Dash",    color: "#8b6cff", s: { set: 22,  growth: 12,  leak: 18,  proof: 75, setup: 100, amount: 8 } },
  };
  const DIMS = [
    { key: "set", label: "Anonymity set" },
    { key: "growth", label: "Grows w/ adoption" },
    { key: "leak", label: "No decoy leakage" },
    { key: "proof", label: "Proof scales" },
    { key: "setup", label: "No trusted setup" },
    { key: "amount", label: "Amount privacy" },
  ];

  /* ================= RADAR ================= */
  const radar = document.getElementById("radar-canvas");
  let radarCmp = "monero";
  function drawRadar() {
    if (!radar) return;
    const ctx = radar.getContext("2d");
    const { w, h, d } = fit(radar);
    ctx.clearRect(0, 0, w, h);
    const cx = w / 2, cy = h / 2 + 6 * d, R = Math.min(w, h) * 0.36;
    const n = DIMS.length;
    const ang = (i) => -Math.PI / 2 + (i * 2 * Math.PI) / n;
    // grid
    for (let g = 1; g <= 4; g++) {
      ctx.beginPath();
      for (let i = 0; i <= n; i++) {
        const a = ang(i % n), rr = (R * g) / 4;
        const x = cx + Math.cos(a) * rr, y = cy + Math.sin(a) * rr;
        i ? ctx.lineTo(x, y) : ctx.moveTo(x, y);
      }
      ctx.strokeStyle = "rgba(255,255,255,0.07)"; ctx.lineWidth = d; ctx.stroke();
    }
    // axes + labels
    ctx.font = `${11 * d}px "Space Grotesk", sans-serif`;
    DIMS.forEach((dim, i) => {
      const a = ang(i);
      ctx.beginPath(); ctx.moveTo(cx, cy);
      ctx.lineTo(cx + Math.cos(a) * R, cy + Math.sin(a) * R);
      ctx.strokeStyle = "rgba(255,255,255,0.07)"; ctx.stroke();
      const lx = cx + Math.cos(a) * (R + 16 * d), ly = cy + Math.sin(a) * (R + 16 * d);
      ctx.fillStyle = "#969cb6";
      ctx.textAlign = Math.abs(Math.cos(a)) < 0.3 ? "center" : Math.cos(a) > 0 ? "left" : "right";
      ctx.textBaseline = "middle";
      ctx.fillText(dim.label, lx, ly);
    });
    const plot = (coin, prog) => {
      ctx.beginPath();
      DIMS.forEach((dim, i) => {
        const a = ang(i), v = (coin.s[dim.key] / 100) * prog;
        const x = cx + Math.cos(a) * R * v, y = cy + Math.sin(a) * R * v;
        i ? ctx.lineTo(x, y) : ctx.moveTo(x, y);
      });
      ctx.closePath();
      ctx.fillStyle = coin.color + "26"; ctx.fill();
      ctx.strokeStyle = coin.color; ctx.lineWidth = 2 * d; ctx.stroke();
      DIMS.forEach((dim, i) => {
        const a = ang(i), v = (coin.s[dim.key] / 100) * prog;
        ctx.beginPath(); ctx.arc(cx + Math.cos(a) * R * v, cy + Math.sin(a) * R * v, 3 * d, 0, 7);
        ctx.fillStyle = coin.color; ctx.fill();
      });
    };
    return { ctx, plot };
  }
  let radarT0 = 0;
  function animRadar() {
    if (!radar) return;
    const lg = document.getElementById("legend-cmp");
    if (lg) lg.innerHTML = `<i style="background:${COINS[radarCmp].color}"></i> ${COINS[radarCmp].name}`;
    radarT0 = performance.now();
    const step = (ts) => {
      const p = ease(Math.min(1, (ts - radarT0) / 700));
      const ctx = drawRadar();
      if (ctx) { ctx.plot(COINS[radarCmp], p); ctx.plot(COINS.obscura, p); }
      if (p < 1) requestAnimationFrame(step);
    };
    requestAnimationFrame(step);
  }

  /* ================= BARS (per dimension) ================= */
  const barsEl = document.getElementById("bars");
  let dimKey = "set", barT0 = 0;
  const order = ["obscura", "zcash", "monero", "dash", "bitcoin"];
  function buildBars() {
    if (!barsEl) return;
    barsEl.innerHTML = order.map((k) => {
      const c = COINS[k];
      return `<div class="bar-row"><span class="bar-name" style="color:${k === "obscura" ? c.color : "#cfd3e6"}">${c.name}</span>
        <div class="bar-track"><div class="bar-fill" data-k="${k}" style="background:${c.color}"></div></div>
        <span class="bar-val" data-k="${k}">0</span></div>`;
    }).join("");
  }
  function animBars() {
    if (!barsEl) return;
    barT0 = performance.now();
    const fills = barsEl.querySelectorAll(".bar-fill");
    const vals = barsEl.querySelectorAll(".bar-val");
    const step = (ts) => {
      const p = ease(Math.min(1, (ts - barT0) / 800));
      fills.forEach((f) => { const t = COINS[f.dataset.k].s[dimKey]; f.style.width = (t * p) + "%"; });
      vals.forEach((v) => { const t = COINS[v.dataset.k].s[dimKey]; v.textContent = Math.round(t * p); });
      if (p < 1) requestAnimationFrame(step);
    };
    requestAnimationFrame(step);
  }

  /* dimension tabs */
  const dimTabs = document.getElementById("dim-tabs");
  if (dimTabs) {
    dimTabs.innerHTML = DIMS.map((d, i) =>
      `<button class="chip ${i === 0 ? "active" : ""}" data-dim="${d.key}">${d.label}</button>`).join("");
    dimTabs.addEventListener("click", (e) => {
      const b = e.target.closest(".chip"); if (!b) return;
      dimKey = b.dataset.dim;
      dimTabs.querySelectorAll(".chip").forEach((c) => c.classList.remove("active"));
      b.classList.add("active");
      animBars();
    });
  }
  /* coin tabs for radar */
  const coinTabs = document.getElementById("coin-tabs");
  if (coinTabs) {
    coinTabs.innerHTML = ["monero", "zcash", "bitcoin", "dash"].map((k, i) =>
      `<button class="chip ${i === 0 ? "active" : ""}" data-coin="${k}" style="--cc:${COINS[k].color}">vs ${COINS[k].name}</button>`).join("");
    coinTabs.addEventListener("click", (e) => {
      const b = e.target.closest(".chip"); if (!b) return;
      radarCmp = b.dataset.coin;
      coinTabs.querySelectorAll(".chip").forEach((c) => c.classList.remove("active"));
      b.classList.add("active");
      animRadar();
    });
  }

  /* ================= GROWTH (log scale) ================= */
  const growth = document.getElementById("growth-canvas");
  const growthSlider = document.getElementById("growth-slider");
  const growthRead = document.getElementById("growth-read");
  // anonymity-set size as a function of adoption x in [0,1]
  function setSize(coin, x) {
    switch (coin) {
      case "obscura": return 1e3 * Math.pow(5e7 / 1e3, x);   // = whole UTXO set, explodes
      case "zcash":   return 1e2 * Math.pow(2e5 / 1e2, x);   // shielded pool grows w/ usage
      case "monero":  return 11 + 5 * x;                      // ring 11 -> 16, ~flat
      case "dash":    return 8 + 8 * x;                        // small mixing set
      case "bitcoin": return 1;                                // transparent
    }
  }
  const Y0 = 0, Y1 = 8; // log10 range (1 .. 1e8)
  function fmtBig(n) {
    if (n >= 1e6) return (n / 1e6).toFixed(n >= 1e7 ? 0 : 1) + "M";
    if (n >= 1e3) return (n / 1e3).toFixed(0) + "k";
    return Math.round(n).toString();
  }
  function drawGrowth(xMark) {
    if (!growth) return;
    const ctx = growth.getContext("2d");
    const { w, h, d } = fit(growth);
    ctx.clearRect(0, 0, w, h);
    const padL = 42 * d, padB = 26 * d, padT = 14 * d, padR = 12 * d;
    const X = (x) => padL + x * (w - padL - padR);
    const Y = (v) => (h - padB) - ((Math.log10(Math.max(1, v)) - Y0) / (Y1 - Y0)) * (h - padB - padT);
    // gridlines (log decades)
    ctx.font = `${10 * d}px ui-monospace, monospace`; ctx.textBaseline = "middle";
    for (let e = Y0; e <= Y1; e += 2) {
      const y = Y(Math.pow(10, e));
      ctx.strokeStyle = "rgba(255,255,255,0.06)"; ctx.lineWidth = d;
      ctx.beginPath(); ctx.moveTo(padL, y); ctx.lineTo(w - padR, y); ctx.stroke();
      ctx.fillStyle = "#6c7290"; ctx.textAlign = "right";
      ctx.fillText("10^" + e, padL - 6 * d, y);
    }
    const coins = ["bitcoin", "dash", "monero", "zcash", "obscura"];
    coins.forEach((k) => {
      ctx.beginPath();
      for (let i = 0; i <= 100; i++) {
        const x = i / 100, v = setSize(k, x);
        i ? ctx.lineTo(X(x), Y(v)) : ctx.moveTo(X(x), Y(v));
      }
      ctx.strokeStyle = COINS[k].color; ctx.lineWidth = (k === "obscura" ? 3 : 2) * d;
      ctx.globalAlpha = k === "obscura" ? 1 : 0.85; ctx.stroke(); ctx.globalAlpha = 1;
    });
    // marker
    const mx = X(xMark);
    ctx.strokeStyle = "rgba(255,255,255,0.25)"; ctx.setLineDash([4 * d, 4 * d]);
    ctx.beginPath(); ctx.moveTo(mx, padT); ctx.lineTo(mx, h - padB); ctx.stroke(); ctx.setLineDash([]);
    coins.forEach((k) => {
      const v = setSize(k, xMark);
      ctx.beginPath(); ctx.arc(mx, Y(v), 3.5 * d, 0, 7); ctx.fillStyle = COINS[k].color; ctx.fill();
    });
    ctx.fillStyle = "#6c7290"; ctx.textAlign = "right";
    ctx.fillText("adoption →", w - padR, h - padB + 16 * d);
  }
  function updateGrowthRead(x) {
    if (!growthRead) return;
    growthRead.innerHTML = ["obscura", "zcash", "monero", "dash", "bitcoin"].map((k) =>
      `<span class="gr" style="color:${COINS[k].color}">${COINS[k].name}: <b>${fmtBig(setSize(k, x))}</b></span>`).join("");
  }
  if (growth) {
    const redraw = () => { const x = growthSlider ? growthSlider.value / 100 : 1; drawGrowth(x); updateGrowthRead(x); };
    redraw();
    if (growthSlider) growthSlider.addEventListener("input", redraw);
    window.addEventListener("resize", redraw);
  }

  /* ================= TRACE THE MONEY ================= */
  const trace = document.getElementById("trace-canvas");
  if (trace) {
    const ctx = trace.getContext("2d");
    let w, h, d, nodes = [], target = 0, phase = 0, t0 = 0, traced = { ring: 0, obx: 0 };
    const meterRing = document.getElementById("trace-ring");
    const meterObx = document.getElementById("trace-obx");
    function size() { const f = fit(trace); w = f.w; h = f.h; d = f.d; layout(); }
    function layout() {
      nodes = [];
      const per = 60, cols = 10, rows = 6;
      const half = w / 2;
      for (let side = 0; side < 2; side++) {
        const ox = side * half;
        const padX = 26 * d, padY = 30 * d;
        const gx = (half - padX * 2) / (cols - 1), gy = (h - padY * 2) / (rows - 1);
        for (let i = 0; i < per; i++) {
          const c = i % cols, r = Math.floor(i / cols);
          nodes.push({ side, x: ox + padX + c * gx, y: padY + r * gy, ph: Math.random() * 7 });
        }
      }
      target = 25; // index within each side's 60
    }
    function draw(ts) {
      ctx.clearRect(0, 0, w, h);
      // divider
      ctx.strokeStyle = "rgba(255,255,255,0.1)"; ctx.lineWidth = d;
      ctx.beginPath(); ctx.moveTo(w / 2, 8 * d); ctx.lineTo(w / 2, h - 8 * d); ctx.stroke();
      const el = (ts - t0) / 1000;
      // ring side suspects (leak): a few highlighted as "probable"
      const ringSuspects = new Set([target, target + 1, target - 1, target + 10, target - 9]);
      nodes.forEach((nd, i) => {
        const idx = i % 60, side = nd.side;
        const pulse = 0.5 + 0.5 * Math.sin(ts / 600 + nd.ph);
        let color = "rgba(120,124,150,0.22)", R = 3 * d;
        if (phase >= 1) {
          if (side === 0) {
            // ring coin: leakage narrows to suspects (red), rest dim
            if (ringSuspects.has(idx)) {
              const a = Math.min(1, el * 1.5);
              color = `rgba(255,90,90,${0.4 + 0.5 * a})`; R = (3 + 2 * a) * d;
            }
          } else {
            // obscura: ENTIRE set lights identical cold — trace fails
            const a = Math.min(1, el * 1.5);
            color = `rgba(0,230,195,${0.3 + 0.35 * pulse * a})`; R = (3 + pulse) * d;
          }
        }
        ctx.beginPath(); ctx.arc(nd.x, nd.y, R, 0, 7); ctx.fillStyle = color; ctx.fill();
      });
      // labels
      ctx.font = `${12 * d}px "Space Grotesk", sans-serif`; ctx.textBaseline = "top";
      ctx.textAlign = "center";
      ctx.fillStyle = "rgba(255,90,90,0.9)"; ctx.fillText("Ring-signature coin", w / 4, 8 * d);
      ctx.fillStyle = "#00e6c3"; ctx.fillText("Obscura", (3 * w) / 4, 8 * d);
      // animate meters
      if (phase >= 1) {
        const a = Math.min(1, el);
        traced.ring = Math.round(72 * a); // analyst narrows to ~5 of 60 → high confidence
        traced.obx = 0;
        if (meterRing) { meterRing.style.width = traced.ring + "%"; meterRing.textContent = traced.ring + "% traced"; }
        if (meterObx) { meterObx.style.width = "2%"; meterObx.textContent = "untraceable"; }
      }
      requestAnimationFrame(draw);
    }
    size(); requestAnimationFrame(draw);
    const btn = document.getElementById("trace-go");
    if (btn) btn.addEventListener("click", () => { phase = 1; t0 = performance.now(); });
    trace.addEventListener("click", () => { phase = 1; t0 = performance.now(); });
    window.addEventListener("resize", size);
  }

  /* kick off radar+bars when scrolled into view (so the anim is seen) */
  const strength = document.getElementById("strength");
  if (strength) {
    const once = new IntersectionObserver((es) => {
      es.forEach((e) => { if (e.isIntersecting) { buildBars(); animBars(); animRadar(); once.disconnect(); } });
    }, { threshold: 0.25 });
    once.observe(strength);
    window.addEventListener("resize", () => { drawRadar() && null; });
  }
})();
