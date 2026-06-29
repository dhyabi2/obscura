// Vercel serverless proxy for the Obscura explorer + web wallet. The browser
// calls this same-origin (HTTPS); we forward to the node's RPC server-side over
// HTTP, so there is no CORS or mixed-content issue and the node's IP stays hidden
// (configured via the NODE_RPC environment variable in the Vercel project).
//
// Only an explicit whitelist of read paths (GET) and two write paths (POST,
// submittx/offer) are forwarded — no open SSRF surface. The wallet is
// non-custodial: it signs locally and only ever sends already-signed hex here.
export default async function handler(req, res) {
  const base = process.env.NODE_RPC;
  if (!base) {
    res.status(500).json({ error: "NODE_RPC env not configured" });
    return;
  }
  const root = base.replace(/\/$/, "");
  const path = String(req.query.path || "");

  // GET read endpoints
  const get = {
    summary: "/explorer/summary",
    mempool: "/explorer/mempool",
    vaults: "/explorer/vaults",
    height: "/height",
    feerate: "/feerate",
    offers: "/offers",
    offersjson: "/offers/json",
    pricehistory: "/explorer/pricehistory",
    swaps: "/explorer/swaps",
    liquidity: "/liquidity",
    swapsactive: "/swaps/active",
    autoliquidity: "/auto-liquidity",
    // NOTE (audit S3a): /xno/account is DELIBERATELY NOT proxied here. On the SHARED
    // public node it returns the OPERATOR's real nano_ proceeds address, which lets
    // anyone trace the operator's XNO on the Nano chain (cross-chain deanonymization).
    // The local desktop-app proxy (cmd/obscura-node/ui.go) still exposes it because
    // there it is the USER's OWN node showing the USER's OWN proceeds. The
    // operator-gated /xno/recovery and /xno/withdraw are likewise never proxied here.
  };
  // Matching-engine market data: trades tape, candles, 24h stats, order status.
  // These carry query params (pair/limit/interval/maker/id), forwarded verbatim
  // from the request's own query string (minus our routing `path`).
  const getQ = {
    trades: "/trades",
    candles: "/candles",
    stats: "/stats",
    orders: "/orders",
  };
  // POST write endpoints (body forwarded as-is)
  const post = {
    submittx: "/submittx",
    offer: "/offer",
    swapstake: "/swaps/take",
  };

  let url, method, body;
  if (path === "block") {
    // privacy-redacted explorer block view (for the explorer UI)
    const h = String(req.query.height || "0").replace(/[^0-9]/g, "");
    url = root + "/explorer/block?height=" + h;
    method = "GET";
  } else if (path === "rawblock") {
    // full serialized block (hex) — the wallet needs this to scan for its outputs
    const h = String(req.query.height || "0").replace(/[^0-9]/g, "");
    url = root + "/block?height=" + h;
    method = "GET";
  } else if (path === "rawblocks") {
    // RANGE of full serialized blocks (hex) so the wallet scans in batches instead of
    // one request per height (far fewer round-trips through this shared proxy).
    const from = String(req.query.from || "0").replace(/[^0-9]/g, "");
    let count = parseInt(String(req.query.count || "256").replace(/[^0-9]/g, ""), 10) || 256;
    if (count > 256) count = 256;
    url = root + "/blocks?from=" + from + "&count=" + count;
    method = "GET";
  } else if (get[path]) {
    url = root + get[path];
    method = "GET";
  } else if (getQ[path]) {
    // forward sanitized query params (pair/limit/interval/maker) to the node.
    const q = new URLSearchParams();
    for (const k of ["pair", "limit", "interval", "maker"]) {
      const v = req.query[k];
      if (v != null) q.set(k, String(v).slice(0, 64).replace(/[^A-Za-z0-9/_.\-]/g, ""));
    }
    const qs = q.toString();
    url = root + getQ[path] + (qs ? "?" + qs : "");
    method = "GET";
  } else if (path === "order") {
    // per-order status: /order/<hexid>
    const id = String(req.query.id || "").replace(/[^0-9a-fA-F]/g, "").slice(0, 64);
    url = root + "/order/" + id;
    method = "GET";
  } else if (post[path]) {
    if (req.method !== "POST") {
      res.status(405).json({ error: "POST required" });
      return;
    }
    url = root + post[path];
    method = "POST";
    body = typeof req.body === "string" ? req.body : JSON.stringify(req.body || {});
  } else {
    res.status(400).json({ error: "unknown path" });
    return;
  }

  try {
    // PRIVACY / IP MINIMIZATION (audit #14/#18): we build the upstream request headers
    // from scratch and DELIBERATELY do NOT pass through any client request headers —
    // notably x-forwarded-for, x-real-ip, forwarded, x-vercel-forwarded-for, cookie,
    // user-agent, referer. The operator's node therefore never receives (and never has to
    // be trusted with) the visitor's IP or browser fingerprint. We also do not log the
    // client IP anywhere in this function.
    // NOTE: Vercel's edge still terminates the TLS connection, so Vercel (the host) can
    // see client IPs; this only stops the IP reaching the operator node. For full
    // anonymity, run your own node and point the wallet at it (see the wallet trust banner).
    const opts = { method, headers: {}, signal: AbortSignal.timeout(8000) };
    if (method === "POST") {
      opts.headers["Content-Type"] = "application/json";
      opts.body = body;
    }
    const r = await fetch(url, opts);
    const text = await r.text();
    res.setHeader("Content-Type", "application/json");
    if (method === "GET") res.setHeader("Cache-Control", "public, max-age=2, s-maxage=2");
    res.status(r.status).send(text);
  } catch (e) {
    res.status(502).json({ error: "node unreachable", detail: String(e) });
  }
}
