/* Obscura site interactivity — vanilla JS, no dependencies. */
(function () {
  "use strict";

  /* ---- mobile nav ---- */
  const toggle = document.querySelector(".nav-toggle");
  const links = document.querySelector(".nav-links");
  if (toggle && links) {
    toggle.addEventListener("click", () => links.classList.toggle("open"));
    links.querySelectorAll("a").forEach((a) =>
      a.addEventListener("click", () => links.classList.remove("open"))
    );
  }

  /* ---- year ---- */
  const yr = document.getElementById("year");
  if (yr) yr.textContent = new Date().getFullYear();

  /* ---- scroll reveal ---- */
  const io = new IntersectionObserver(
    (entries) => {
      entries.forEach((e) => {
        if (e.isIntersecting) {
          e.target.classList.add("in");
          io.unobserve(e.target);
        }
      });
    },
    { threshold: 0.12 }
  );
  document.querySelectorAll(".reveal").forEach((el) => io.observe(el));

  /* ---- copy buttons ---- */
  document.querySelectorAll(".copy").forEach((btn) => {
    btn.addEventListener("click", () => {
      const code = btn.parentElement.querySelector("pre");
      const text = (code ? code.innerText : "").replace(/^\s*#.*$/gm, (m) => m); // keep as-is
      navigator.clipboard.writeText(code ? code.innerText : "").then(() => {
        const old = btn.textContent;
        btn.textContent = "copied ✓";
        setTimeout(() => (btn.textContent = old), 1400);
      });
    });
  });

  /* ---- step tabs ---- */
  const tabs = document.querySelectorAll(".step-tab");
  const panes = document.querySelectorAll(".step-panel .pane");
  tabs.forEach((tab) =>
    tab.addEventListener("click", () => {
      tabs.forEach((t) => t.classList.remove("active"));
      panes.forEach((p) => p.classList.remove("active"));
      tab.classList.add("active");
      const pane = document.getElementById("pane-" + tab.dataset.step);
      if (pane) pane.classList.add("active");
    })
  );

  /* ---- hero particle field (an "anonymity set" of drifting nodes) ---- */
  const hero = document.getElementById("hero-canvas");
  if (hero) {
    const ctx = hero.getContext("2d");
    let w, h, pts, raf;
    const N = 130;
    function size() {
      const r = hero.getBoundingClientRect();
      w = hero.width = r.width * devicePixelRatio;
      h = hero.height = r.height * devicePixelRatio;
    }
    function init() {
      pts = Array.from({ length: N }, () => ({
        x: Math.random() * w,
        y: Math.random() * h,
        vx: (Math.random() - 0.5) * 0.25 * devicePixelRatio,
        vy: (Math.random() - 0.5) * 0.25 * devicePixelRatio,
        r: (Math.random() * 1.6 + 0.6) * devicePixelRatio,
      }));
    }
    function frame() {
      ctx.clearRect(0, 0, w, h);
      for (let i = 0; i < N; i++) {
        const p = pts[i];
        p.x += p.vx; p.y += p.vy;
        if (p.x < 0 || p.x > w) p.vx *= -1;
        if (p.y < 0 || p.y > h) p.vy *= -1;
        for (let j = i + 1; j < N; j++) {
          const q = pts[j];
          const dx = p.x - q.x, dy = p.y - q.y;
          const d = Math.hypot(dx, dy);
          const max = 130 * devicePixelRatio;
          if (d < max) {
            ctx.strokeStyle = `rgba(139,108,255,${(1 - d / max) * 0.18})`;
            ctx.lineWidth = devicePixelRatio;
            ctx.beginPath(); ctx.moveTo(p.x, p.y); ctx.lineTo(q.x, q.y); ctx.stroke();
          }
        }
      }
      for (const p of pts) {
        ctx.fillStyle = "rgba(0,230,195,0.7)";
        ctx.beginPath(); ctx.arc(p.x, p.y, p.r, 0, 7); ctx.fill();
      }
      raf = requestAnimationFrame(frame);
    }
    size(); init(); frame();
    addEventListener("resize", () => { cancelAnimationFrame(raf); size(); init(); frame(); });
  }

  /* ---- interactive anonymity-set visualizer ---- */
  const anon = document.getElementById("anon-canvas");
  if (anon) {
    const ctx = anon.getContext("2d");
    const slider = document.getElementById("anon-size");
    const label = document.getElementById("anon-label");
    const spendBtn = document.getElementById("anon-spend");
    const modeLabel = document.getElementById("anon-mode");
    let w, h, nodes = [], realIdx = 0, ringMode = false, t0 = -10, particles = [];

    function size() {
      const r = anon.getBoundingClientRect();
      w = anon.width = r.width * devicePixelRatio;
      h = anon.height = r.height * devicePixelRatio;
    }
    function build() {
      const n = parseInt(slider.value, 10);
      label.textContent = n;
      nodes = [];
      const cols = Math.ceil(Math.sqrt(n * (w / h)));
      const rows = Math.ceil(n / cols);
      const padX = w * 0.08, padY = h * 0.12;
      const gx = (w - padX * 2) / Math.max(1, cols - 1);
      const gy = (h - padY * 2) / Math.max(1, rows - 1);
      for (let i = 0; i < n; i++) {
        const c = i % cols, r = Math.floor(i / cols);
        nodes.push({ x: padX + c * gx, y: padY + r * gy, ph: Math.random() * 7 });
      }
      realIdx = Math.floor(n / 2) + 3;
      if (realIdx >= n) realIdx = n - 1;
    }
    function draw(ts) {
      ctx.clearRect(0, 0, w, h);
      const ringSet = new Set();
      if (ringMode) {
        // Monero-style: only ~16 decoys "could" be the spender
        ringSet.add(realIdx);
        let added = 0;
        while (added < Math.min(15, nodes.length - 1)) {
          const k = Math.floor(Math.random() * nodes.length);
          if (!ringSet.has(k)) { ringSet.add(k); added++; }
        }
      }
      const elapsed = (ts - t0) / 1000;
      nodes.forEach((nd, i) => {
        const inSet = !ringMode || ringSet.has(i);
        const pulse = 0.5 + 0.5 * Math.sin(ts / 600 + nd.ph);
        let R = 3.2 * devicePixelRatio;
        let color;
        if (!inSet) {
          color = "rgba(120,124,150,0.18)";
        } else {
          // every output in the set is indistinguishable — same look
          const a = 0.45 + pulse * 0.4;
          color = `rgba(0,230,195,${a})`;
          R = (3.4 + pulse * 1.2) * devicePixelRatio;
        }
        ctx.fillStyle = color;
        ctx.beginPath(); ctx.arc(nd.x, nd.y, R, 0, 7); ctx.fill();
      });
      // DISSOLVE-TO-POOL: your output flares, then scatters into the crowd and
      // becomes indistinguishable — "spending" is a disappearing act.
      if (elapsed >= 0 && elapsed < 1.8 && nodes[realIdx]) {
        const nd = nodes[realIdx];
        // expanding ripple
        const rr = elapsed * Math.max(w, h) * 0.6;
        ctx.strokeStyle = `rgba(139,108,255,${Math.max(0, 0.9 - elapsed / 1.8)})`;
        ctx.lineWidth = 2 * devicePixelRatio;
        ctx.beginPath(); ctx.arc(nd.x, nd.y, rr, 0, 7); ctx.stroke();
        // the real output flares violet then fades to identical
        const fade = Math.max(0, 1 - elapsed / 0.9);
        if (fade > 0) {
          ctx.fillStyle = `rgba(139,108,255,${fade})`;
          ctx.beginPath(); ctx.arc(nd.x, nd.y, (5 + fade * 6) * devicePixelRatio, 0, 7); ctx.fill();
        }
        // scattering particles
        particles.forEach((p) => {
          p.x += p.vx; p.y += p.vy; p.life -= 0.02;
          if (p.life > 0) {
            ctx.fillStyle = `rgba(0,230,195,${p.life})`;
            ctx.beginPath(); ctx.arc(p.x, p.y, 2 * devicePixelRatio, 0, 7); ctx.fill();
          }
        });
      }
      requestAnimationFrame(draw);
    }
    size(); build(); t0 = performance.now(); requestAnimationFrame(draw);
    slider.addEventListener("input", build);
    spendBtn.addEventListener("click", () => {
      t0 = performance.now();
      // emit scatter particles from the real output
      particles = [];
      const nd = nodes[realIdx];
      if (nd) for (let i = 0; i < 28; i++) {
        const a = (i / 28) * Math.PI * 2, sp = (1 + Math.random() * 2.4) * devicePixelRatio;
        particles.push({ x: nd.x, y: nd.y, vx: Math.cos(a) * sp, vy: Math.sin(a) * sp, life: 1 });
      }
    });
    if (modeLabel) {
      modeLabel.addEventListener("click", () => {
        ringMode = !ringMode;
        modeLabel.textContent = ringMode ? "Showing: ring of 16 (Monero-style)" : "Showing: global set (Obscura)";
        modeLabel.classList.toggle("ring", ringMode);
      });
    }
    addEventListener("resize", () => { size(); build(); });
  }

  /* ---- emission curve ---- */
  const em = document.getElementById("emission-canvas");
  if (em) {
    const ctx = em.getContext("2d");
    function size() {
      const r = em.getBoundingClientRect();
      em.width = r.width * devicePixelRatio;
      em.height = r.height * devicePixelRatio;
    }
    function draw() {
      const w = em.width, h = em.height, pad = 34 * devicePixelRatio;
      ctx.clearRect(0, 0, w, h);
      // axes
      ctx.strokeStyle = "rgba(255,255,255,0.12)"; ctx.lineWidth = devicePixelRatio;
      ctx.beginPath(); ctx.moveTo(pad, pad / 2); ctx.lineTo(pad, h - pad); ctx.lineTo(w - pad / 2, h - pad); ctx.stroke();
      // emission ~ remaining >> 19 with tail floor; model cumulative supply
      const cap = 18.4e6, tail = 0.6, shift = 19;
      let emitted = 0; const pts = [];
      const blocks = 4_500_000; // ~17 years at 120s
      for (let b = 0; b <= blocks; b += blocks / 240) {
        // approximate by stepping the closed form
        let rem = cap - emitted;
        let reward = rem / (2 ** shift);
        if (reward < tail) reward = tail;
        pts.push({ b, reward, emitted });
        // advance emitted over the step
        const stepBlocks = blocks / 240;
        emitted += reward * stepBlocks;
        if (emitted > cap) emitted = cap;
      }
      const maxR = pts[0].reward;
      // reward curve (teal)
      ctx.strokeStyle = "#00e6c3"; ctx.lineWidth = 2.4 * devicePixelRatio;
      ctx.beginPath();
      pts.forEach((p, i) => {
        const x = pad + (p.b / blocks) * (w - pad * 1.5);
        const y = (h - pad) - (p.reward / maxR) * (h - pad * 1.5);
        i ? ctx.lineTo(x, y) : ctx.moveTo(x, y);
      });
      ctx.stroke();
      // tail floor line
      const ty = (h - pad) - (tail / maxR) * (h - pad * 1.5);
      ctx.strokeStyle = "rgba(255,122,217,0.6)"; ctx.setLineDash([5 * devicePixelRatio, 5 * devicePixelRatio]);
      ctx.beginPath(); ctx.moveTo(pad, ty); ctx.lineTo(w - pad / 2, ty); ctx.stroke(); ctx.setLineDash([]);
      // labels
      ctx.fillStyle = "#969cb6"; ctx.font = `${11 * devicePixelRatio}px ui-monospace, monospace`;
      ctx.fillText("block reward", pad + 6 * devicePixelRatio, pad);
      ctx.fillStyle = "rgba(255,122,217,0.9)";
      ctx.fillText("tail: 0.6 OBX / block (forever)", pad + 6 * devicePixelRatio, ty - 6 * devicePixelRatio);
      ctx.fillStyle = "#6c7290";
      ctx.fillText("time →", w - pad * 2.2, h - pad + 18 * devicePixelRatio);
    }
    size(); draw();
    addEventListener("resize", () => { size(); draw(); });
  }

  /* ---- docs sidebar active section ---- */
  const docHeads = document.querySelectorAll(".docs-main h2[id]");
  const docLinks = document.querySelectorAll(".docs-side a");
  if (docHeads.length && docLinks.length) {
    const dio = new IntersectionObserver(
      (entries) => {
        entries.forEach((e) => {
          if (e.isIntersecting) {
            docLinks.forEach((l) => l.classList.remove("active"));
            const a = document.querySelector(`.docs-side a[href="#${e.target.id}"]`);
            if (a) a.classList.add("active");
          }
        });
      },
      { rootMargin: "-80px 0px -70% 0px" }
    );
    docHeads.forEach((h) => dio.observe(h));
  }
})();
