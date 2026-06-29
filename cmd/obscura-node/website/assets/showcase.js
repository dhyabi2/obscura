/* Obscura — feature showcase carousel. One canvas, a draw() per feature, autoplay
   with manual controls. Each slide is a small live simulation of a feature. */
(function () {
  "use strict";
  const stage = document.getElementById("show-canvas");
  if (!stage) return;
  const ctx = stage.getContext("2d");
  const elTag = document.getElementById("show-tag");
  const elTitle = document.getElementById("show-title");
  const elDesc = document.getElementById("show-desc");
  const elBar = document.getElementById("show-bar");
  const elDots = document.getElementById("show-dots");
  const TEAL = "#00e6c3", VIOLET = "#8b6cff", PINK = "#ff7ad9", GOLD = "#f4b728", MUTE = "rgba(150,156,182,.4)";
  const AUTOPLAY = 5000;

  let W, H, D;
  function fit() {
    const r = stage.getBoundingClientRect();
    D = Math.min(window.devicePixelRatio || 1, 2);
    W = stage.width = Math.max(1, r.width * D);
    H = stage.height = Math.max(1, r.height * D);
  }
  const dot = (x, y, r, c) => { ctx.beginPath(); ctx.arc(x, y, r, 0, 7); ctx.fillStyle = c; ctx.fill(); };
  const ring = (x, y, r, c, lw) => { ctx.beginPath(); ctx.arc(x, y, r, 0, 7); ctx.strokeStyle = c; ctx.lineWidth = (lw || 2) * D; ctx.stroke(); };
  const loop = (t, p) => (t % p) / p; // 0..1 progress within period p

  /* ---------- slides ---------- */
  const slides = [
    {
      tag: "Privacy", title: "Global anonymity set",
      desc: "Every spend hides among ALL outputs — not 16 decoys. The set is the whole chain.",
      draw(t) {
        const cols = 16, rows = 9, padX = W * 0.08, padY = H * 0.16;
        const gx = (W - padX * 2) / (cols - 1), gy = (H - padY * 2) / (rows - 1);
        const k = loop(t, 3.2);
        const real = 70;
        let i = 0;
        for (let r = 0; r < rows; r++) for (let c = 0; c < cols; c++, i++) {
          const x = padX + c * gx, y = padY + r * gy;
          const pulse = 0.5 + 0.5 * Math.sin(t * 2 + i);
          dot(x, y, (2.6 + pulse) * D, `rgba(0,230,195,${0.35 + pulse * 0.4})`);
          if (i === real && k < 0.5) {
            const f = 1 - k / 0.5;
            dot(x, y, (3 + f * 7) * D, `rgba(139,108,255,${f})`);
            ring(x, y, k * W * 0.5, `rgba(139,108,255,${f * 0.8})`);
          }
        }
      },
    },
    {
      tag: "Recipient privacy", title: "Stealth addresses",
      desc: "A fresh one-time key for every payment. Your address never appears on-chain.",
      draw(t) {
        const y = H / 2;
        ctx.setLineDash([7 * D, 6 * D]); ctx.lineDashOffset = -(t * 40 * D) % 13;
        ctx.strokeStyle = VIOLET; ctx.lineWidth = 2 * D;
        ctx.beginPath(); ctx.moveTo(W * 0.15, y); ctx.lineTo(W * 0.85, y); ctx.stroke();
        ctx.setLineDash([]);
        dot(W * 0.15, y, 9 * D, PINK);
        ring(W * 0.5, y, 12 * D, MUTE, 2);
        // a NEW one-time key pops periodically at varying height
        const k = loop(t, 1.6), idx = Math.floor(t / 1.6);
        const off = ((idx * 53) % 7 - 3) * 14 * D;
        const f = Math.sin(k * Math.PI);
        dot(W * 0.85, y + off, (5 + f * 5) * D, TEAL);
        ctx.globalAlpha = f; ring(W * 0.85, y + off, (8 + f * 8) * D, TEAL, 2); ctx.globalAlpha = 1;
        labels(["R", "you", "fresh P"], [W * 0.15, W * 0.5, W * 0.85], y + 30 * D);
      },
    },
    {
      tag: "Amount privacy", title: "Confidential amounts",
      desc: "Values are sealed in Pedersen commitments. Range proofs keep them honest.",
      draw(t) {
        const cx = W / 2, cy = H / 2, bw = 150 * D, bh = 64 * D;
        const k = loop(t, 3);
        roundRect(cx - bw / 2, cy - bh / 2, bw, bh, 14 * D, "rgba(0,230,195,.08)", TEAL);
        ctx.textAlign = "center"; ctx.textBaseline = "middle";
        ctx.font = `${20 * D}px ui-monospace, monospace`;
        if (k < 0.5) {
          ctx.fillStyle = "#cfd3e6";
          const digits = String(Math.floor(1000 + Math.random() * 9000));
          ctx.fillText(digits + " OBX", cx, cy);
        } else {
          ctx.fillStyle = TEAL; ctx.fillText("🔒 ••••••", cx, cy);
        }
        ctx.font = `${12 * D}px "Space Grotesk", sans-serif`; ctx.fillStyle = "#969cb6";
        ctx.fillText("C = a·G + v·H", cx, cy + bh / 2 + 22 * D);
      },
    },
    {
      tag: "Trustless", title: "No trusted setup",
      desc: "An accumulator over a group of unknown order — class-group needs no ceremony.",
      draw(t) {
        const cx = W * 0.72, cy = H / 2;
        // outputs stream in
        for (let i = 0; i < 9; i++) {
          const k = loop(t + i * 0.4, 2.2);
          const x = W * 0.12 + k * (cx - W * 0.12);
          const y = cy + Math.sin(i * 1.7) * 50 * D * (1 - k);
          dot(x, y, 3.5 * D, `rgba(0,230,195,${1 - k})`);
        }
        const pulse = 0.5 + 0.5 * Math.sin(t * 3);
        ring(cx, cy, (26 + pulse * 4) * D, VIOLET, 3);
        dot(cx, cy, 16 * D, "rgba(139,108,255,.5)");
        ctx.textAlign = "center"; ctx.fillStyle = "#cfd3e6"; ctx.font = `${12 * D}px ui-monospace`;
        ctx.fillText("accumulator", cx, cy + 44 * D);
      },
    },
    {
      tag: "Wallet", title: "Subaddresses",
      desc: "Unlimited unlinkable receive addresses from a single seed.",
      draw(t) {
        const cx = W * 0.2, cy = H / 2;
        dot(cx, cy, 12 * D, VIOLET);
        ctx.textAlign = "center"; ctx.fillStyle = "#969cb6"; ctx.font = `${11 * D}px ui-monospace`;
        ctx.fillText("seed", cx, cy + 30 * D);
        const n = 7;
        for (let i = 0; i < n; i++) {
          const appear = Math.min(1, Math.max(0, (loop(t, 4) * n) - i));
          const ang = -0.7 + (i / (n - 1)) * 1.4;
          const ex = cx + Math.cos(ang) * (W * 0.55) * appear;
          const ey = cy + Math.sin(ang) * (H * 0.32) * appear;
          ctx.strokeStyle = `rgba(0,230,195,${0.3 * appear})`; ctx.lineWidth = 1.5 * D;
          ctx.beginPath(); ctx.moveTo(cx, cy); ctx.lineTo(ex, ey); ctx.stroke();
          dot(ex, ey, 6 * D * appear, TEAL);
        }
      },
    },
    {
      tag: "Auditing", title: "View-only wallets",
      desc: "Share a view key: an auditor sees incoming funds but can never spend them.",
      draw(t) {
        const cx = W / 2, cy = H / 2;
        // eye
        ctx.strokeStyle = TEAL; ctx.lineWidth = 3 * D;
        ctx.beginPath();
        ctx.ellipse(cx, cy, 70 * D, 38 * D, 0, 0, 7); ctx.stroke();
        dot(cx, cy, 16 * D, "rgba(0,230,195,.6)"); dot(cx, cy, 7 * D, "#06060c");
        // incoming coin falls in
        const k = loop(t, 2);
        dot(cx, cy - 90 * D + k * 90 * D, 6 * D, GOLD);
        // spend blocked
        ctx.textAlign = "center"; ctx.font = `${13 * D}px "Space Grotesk"`;
        ctx.fillStyle = PINK; ctx.fillText("✕ cannot spend", cx, cy + 64 * D);
      },
    },
    {
      tag: "Proofs", title: "Payment proofs",
      desc: "Prove you paid — or were paid — a specific amount. Verifiable fully offline.",
      draw(t) {
        const cx = W / 2, cy = H / 2, bw = 130 * D, bh = 80 * D;
        roundRect(cx - bw / 2, cy - bh / 2, bw, bh, 10 * D, "rgba(255,255,255,.03)", MUTE);
        ctx.strokeStyle = "rgba(150,156,182,.5)"; ctx.lineWidth = 2 * D;
        for (let i = 0; i < 3; i++) { const y = cy - 16 * D + i * 14 * D; ctx.beginPath(); ctx.moveTo(cx - 40 * D, y); ctx.lineTo(cx + 20 * D, y); ctx.stroke(); }
        // animated check
        const k = loop(t, 2.6); const p = Math.min(1, k / 0.6);
        ctx.strokeStyle = TEAL; ctx.lineWidth = 4 * D; ctx.lineCap = "round";
        ctx.beginPath();
        const ax = cx + 6 * D, ay = cy + 22 * D;
        ctx.moveTo(ax - 14 * D, ay - 6 * D);
        if (p > 0) ctx.lineTo(ax - 14 * D + 8 * D * Math.min(1, p / 0.4), ay + 2 * D * Math.min(1, p / 0.4));
        if (p > 0.4) ctx.lineTo(ax - 6 * D + 18 * D * ((p - 0.4) / 0.6), ay + 2 * D - 16 * D * ((p - 0.4) / 0.6));
        ctx.stroke(); ctx.lineCap = "butt";
        ctx.textAlign = "center"; ctx.fillStyle = "#969cb6"; ctx.font = `${11 * D}px ui-monospace`;
        ctx.fillText("DLEQ verified", cx, cy + bh / 2 + 20 * D);
      },
    },
    {
      tag: "Fees", title: "Dynamic fees & replace-by-fee",
      desc: "Smart fee estimation, fee-aware mining, and bump a stuck payment any time.",
      draw(t) {
        const y = H / 2, k = loop(t, 3);
        // track
        ctx.strokeStyle = "rgba(255,255,255,.08)"; ctx.lineWidth = 14 * D;
        ctx.beginPath(); ctx.moveTo(W * 0.12, y); ctx.lineTo(W * 0.88, y); ctx.stroke();
        // slow then bumped fast
        let x;
        if (k < 0.5) x = W * 0.12 + (k / 0.5) * (W * 0.4); // slow crawl
        else x = W * 0.52 + ((k - 0.5) / 0.5) * (W * 0.36); // bump → fast
        const fast = k >= 0.5;
        roundRect(x - 16 * D, y - 9 * D, 32 * D, 18 * D, 5 * D, fast ? "rgba(0,230,195,.85)" : "rgba(139,108,255,.7)", null);
        ctx.textAlign = "center"; ctx.fillStyle = fast ? TEAL : "#969cb6"; ctx.font = `${12 * D}px "Space Grotesk"`;
        ctx.fillText(fast ? "↑ bumped — confirms fast" : "low fee — stuck…", W / 2, y + 38 * D);
        dot(W * 0.88, y, 5 * D, GOLD); // miner
      },
    },
    {
      tag: "Cross-chain", title: "Atomic XMR ↔ OBX swaps",
      desc: "Trustless swaps with Monero via adaptor signatures. No bridge, no custodian.",
      draw(t) {
        const k = loop(t, 3), cy = H / 2;
        const xm = W * 0.2 + k * (W * 0.6), om = W * 0.8 - k * (W * 0.6);
        const arc = Math.sin(k * Math.PI) * 50 * D;
        dot(xm, cy - arc, 14 * D, PINK);
        dot(om, cy + arc, 14 * D, TEAL);
        ctx.textAlign = "center"; ctx.font = `${11 * D}px ui-monospace`;
        ctx.fillStyle = PINK; ctx.fillText("XMR", W * 0.2, cy + 70 * D);
        ctx.fillStyle = TEAL; ctx.fillText("OBX", W * 0.8, cy + 70 * D);
        ctx.fillStyle = "#969cb6"; ctx.fillText("adaptor signatures", W / 2, cy - 76 * D);
      },
    },
    {
      tag: "Security", title: "Encrypted wallet",
      desc: "Seed and state sealed with Argon2id + XChaCha20-Poly1305. Rotate any time.",
      draw(t) {
        const cx = W / 2, cy = H / 2 + 8 * D, k = loop(t, 3);
        const shut = Math.min(1, k / 0.5);
        // body
        roundRect(cx - 34 * D, cy - 6 * D, 68 * D, 52 * D, 9 * D, "rgba(139,108,255,.18)", VIOLET);
        // shackle closing
        ctx.strokeStyle = TEAL; ctx.lineWidth = 6 * D; ctx.lineCap = "round";
        const top = cy - 6 * D - (1 - shut) * 22 * D - 18 * D;
        ctx.beginPath(); ctx.arc(cx, top, 18 * D, Math.PI, 0); ctx.stroke(); ctx.lineCap = "butt";
        ctx.textAlign = "center"; ctx.fillStyle = "#969cb6"; ctx.font = `${11 * D}px ui-monospace`;
        ctx.fillText(shut >= 1 ? "Argon2id · encrypted" : "sealing…", cx, cy + 64 * D);
      },
    },
    {
      tag: "Backup", title: "24-word mnemonic",
      desc: "A checksummed seed phrase recovers your wallet — typos are caught, not cashed.",
      draw(t) {
        const cols = 6, rows = 4, padX = W * 0.14, padY = H * 0.24;
        const gx = (W - padX * 2) / cols, gy = (H - padY * 2) / rows;
        const shown = Math.floor(loop(t, 4) * 25);
        let i = 0;
        for (let r = 0; r < rows; r++) for (let c = 0; c < cols; c++, i++) {
          const x = padX + c * gx, y = padY + r * gy;
          const on = i < shown;
          roundRect(x + 3 * D, y + 3 * D, gx - 8 * D, gy - 8 * D, 6 * D,
            on ? "rgba(0,230,195,.12)" : "rgba(255,255,255,.03)", on ? "rgba(0,230,195,.5)" : "rgba(255,255,255,.07)");
        }
        ctx.textAlign = "center"; ctx.fillStyle = "#969cb6"; ctx.font = `${11 * D}px ui-monospace`;
        ctx.fillText("gabe · hano · gima · …", W / 2, H - padY * 0.5);
      },
    },
    {
      tag: "Network", title: "Tor & Dandelion++",
      desc: "Stem-then-fluff relay and optional onion transport hide where a tx was born.",
      draw(t) {
        const y = H / 2, k = loop(t, 3.2);
        const stem = [W * 0.12, W * 0.32, W * 0.5];
        stem.forEach((x, i) => { dot(x, y, 7 * D, i === 0 ? GOLD : VIOLET); if (i) line(stem[i - 1], y, x, y, VIOLET, 0.5); });
        // packet travels stem
        const seg = Math.min(2.999, k * 4);
        const si = Math.floor(seg), fr = seg - si;
        if (si < 2) { const x = stem[si] + (stem[si + 1] - stem[si]) * fr; dot(x, y, 5 * D, TEAL); }
        else {
          // fluff: burst to many
          for (let j = 0; j < 9; j++) {
            const a = (j / 9) * Math.PI * 2, rr = fr * W * 0.32;
            dot(W * 0.5 + Math.cos(a) * rr, y + Math.sin(a) * rr * 0.7, 4 * D, `rgba(0,230,195,${1 - fr})`);
          }
        }
        ctx.textAlign = "center"; ctx.fillStyle = "#969cb6"; ctx.font = `${11 * D}px ui-monospace`;
        ctx.fillText("stem → fluff", W * 0.31, y + 34 * D);
      },
    },
    {
      tag: "Consensus", title: "RandomX-style VM mining",
      desc: "A memory-hard, randomized-VM PoW (ASIC-resistant). 120s blocks, smooth emission with a perpetual tail.",
      draw(t) {
        const y = H / 2, n = 5, bw = 46 * D, gap = 22 * D;
        const total = n * bw + (n - 1) * gap, x0 = (W - total) / 2;
        const newOne = loop(t, 2.4);
        for (let i = 0; i < n; i++) {
          const x = x0 + i * (bw + gap);
          const isNew = i === n - 1;
          const a = isNew ? newOne : 1;
          roundRect(x, y - 22 * D, bw, 44 * D, 7 * D, `rgba(0,230,195,${0.12 * a})`, `rgba(0,230,195,${0.6 * a})`);
          if (i) line(x0 + i * (bw + gap) - gap, y, x, y, "rgba(255,255,255,.12)", 1);
          if (isNew && newOne > 0.85) { ring(x + bw / 2, y, (newOne - 0.85) * 200 * D, `rgba(244,183,40,${1 - newOne})`); }
        }
        ctx.textAlign = "center"; ctx.fillStyle = "#969cb6"; ctx.font = `${11 * D}px ui-monospace`;
        ctx.fillText("⛏ new block", W / 2, y + 44 * D);
      },
    },
  ];

  /* ---- canvas helpers needing ctx ---- */
  function roundRect(x, y, w, h, r, fill, stroke) {
    ctx.beginPath();
    ctx.moveTo(x + r, y);
    ctx.arcTo(x + w, y, x + w, y + h, r); ctx.arcTo(x + w, y + h, x, y + h, r);
    ctx.arcTo(x, y + h, x, y, r); ctx.arcTo(x, y, x + w, y, r); ctx.closePath();
    if (fill) { ctx.fillStyle = fill; ctx.fill(); }
    if (stroke) { ctx.strokeStyle = stroke; ctx.lineWidth = 2 * D; ctx.stroke(); }
  }
  function line(x1, y1, x2, y2, c, a) { ctx.strokeStyle = c; ctx.globalAlpha = a == null ? 1 : a; ctx.lineWidth = 1.5 * D; ctx.beginPath(); ctx.moveTo(x1, y1); ctx.lineTo(x2, y2); ctx.stroke(); ctx.globalAlpha = 1; }
  function labels(arr, xs, y) { ctx.textAlign = "center"; ctx.fillStyle = "#969cb6"; ctx.font = `${11 * D}px ui-monospace`; arr.forEach((s, i) => ctx.fillText(s, xs[i], y)); }

  /* ---------- controller ---------- */
  let idx = 0, slideStart = 0, lastTick = 0, running = false, paused = false, raf;

  elDots.innerHTML = slides.map((_, i) => `<button class="show-dot ${i === 0 ? "active" : ""}" data-i="${i}" aria-label="Slide ${i + 1}"></button>`).join("");
  const dots = [...elDots.querySelectorAll(".show-dot")];

  function setSlide(i) {
    idx = (i + slides.length) % slides.length;
    const s = slides[idx];
    elTag.textContent = s.tag; elTitle.textContent = s.title; elDesc.textContent = s.desc;
    dots.forEach((d, j) => d.classList.toggle("active", j === idx));
    slideStart = performance.now();
    elTitle.classList.remove("kick"); void elTitle.offsetWidth; elTitle.classList.add("kick");
  }

  function frame(now) {
    raf = requestAnimationFrame(frame);
    if (!running) return;
    const t = (now - slideStart) / 1000;
    // autoplay progress
    if (!paused) {
      const prog = (now - slideStart) / AUTOPLAY;
      elBar.style.width = Math.min(100, prog * 100) + "%";
      if (prog >= 1) { setSlide(idx + 1); }
    }
    ctx.clearRect(0, 0, W, H);
    slides[idx].draw(t, W, H, D);
  }

  fit(); setSlide(0);
  window.addEventListener("resize", fit);
  raf = requestAnimationFrame(frame);

  // run only while visible
  const io = new IntersectionObserver((e) => { running = e[0].isIntersecting; }, { threshold: 0.15 });
  io.observe(stage);
  document.addEventListener("visibilitychange", () => { if (document.hidden) running = false; });

  // controls
  elDots.addEventListener("click", (e) => { const b = e.target.closest(".show-dot"); if (b) setSlide(+b.dataset.i); });
  const prev = document.getElementById("show-prev"), next = document.getElementById("show-next");
  if (prev) prev.addEventListener("click", () => setSlide(idx - 1));
  if (next) next.addEventListener("click", () => setSlide(idx + 1));
  const card = document.getElementById("show-card");
  if (card) {
    let pauseAt = 0;
    card.addEventListener("pointerenter", () => { paused = true; pauseAt = performance.now(); });
    card.addEventListener("pointerleave", () => { if (paused) slideStart += performance.now() - pauseAt; paused = false; });
  }
})();
