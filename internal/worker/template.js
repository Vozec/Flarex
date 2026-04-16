// FlareX worker template (V2 — optimized).
// Wire protocol unchanged: HMAC payload `${ts}|${host}|${port}|${tls}|${mode}`,
// query params h/p/ts/s/t/m, WebSocket upgrade required.
import { connect } from 'cloudflare:sockets';

const HMAC_SECRET = "__HMAC_SECRET__";
const TS_WINDOW_SEC = 60;

// --- Hot-path singletons (V8 keeps these as monomorphic call sites). ---
const ENC = new TextEncoder();
const DEC = new TextDecoder();
const PROBE_BYTE = new Uint8Array([0x01]);
const HEADER_TERMINATOR = new Uint8Array([13, 10, 13, 10]); // \r\n\r\n
const STRIP_RESPONSE_HEADERS = new Set(["content-encoding", "transfer-encoding", "content-length"]);

// HMAC key imported once at cold start; reused for every dial.
// Saves ~0.5 ms of crypto.subtle.importKey per request.
let HMAC_KEY_PROMISE = null;
function hmacKey() {
  if (!HMAC_KEY_PROMISE) {
    HMAC_KEY_PROMISE = crypto.subtle.importKey(
      "raw",
      ENC.encode(HMAC_SECRET),
      { name: "HMAC", hash: "SHA-256" },
      false,
      ["sign", "verify"],
    );
  }
  return HMAC_KEY_PROMISE;
}

// hex2bytes converts the client-supplied lowercase hex signature to raw
// bytes for crypto.subtle.verify (constant-time + no hex re-encode of our
// own MAC). Tolerant of upper/lower case; rejects non-hex.
function hex2bytes(hex) {
  if (hex.length % 2 !== 0) return null;
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < out.length; i++) {
    const hi = parseInt(hex[i * 2], 16);
    const lo = parseInt(hex[i * 2 + 1], 16);
    if (Number.isNaN(hi) || Number.isNaN(lo)) return null;
    out[i] = (hi << 4) | lo;
  }
  return out;
}

function bad(status, msg) {
  return new Response(msg, { status });
}

// --- Header read: no O(N²) re-concat. ---
//
// Keeps a list of chunks and only scans the freshly-arrived bytes (with a
// small overlap) for the \r\n\r\n terminator. The full buffer is only
// materialised once we find the boundary — and split into headerBytes +
// leftover with subarray, no copy.
async function readUntilHeadersEnd(reader, max) {
  const chunks = [];
  let total = 0;
  let scanned = 0; // index into the conceptual flat buffer where we resumed scanning

  while (total < max) {
    const { value, done } = await reader.read();
    if (done) break;
    chunks.push(value);
    total += value.length;

    // Search for \r\n\r\n with a 3-byte overlap into the previous chunk to
    // catch terminators that straddle a chunk boundary.
    const flat = chunks.length === 1 ? chunks[0] : concat(chunks);
    const start = Math.max(0, scanned - 3);
    const idx = indexOfBoundary(flat, start);
    if (idx >= 0) {
      return { headerBytes: flat.subarray(0, idx + 4), leftover: flat.subarray(idx + 4) };
    }
    scanned = total;
    // If we've been growing the chunks list a lot, collapse to a single
    // buffer so the next scan is on contiguous memory (still O(N) total).
    if (chunks.length > 4) {
      chunks.length = 0;
      chunks.push(flat);
    }
  }
  throw new Error("headers not terminated");
}

function concat(chunks) {
  let total = 0;
  for (const c of chunks) total += c.length;
  const out = new Uint8Array(total);
  let off = 0;
  for (const c of chunks) { out.set(c, off); off += c.length; }
  return out;
}

// Specialised \r\n\r\n search; faster than the generic indexOf because the
// inner loop is unrolled to 4 fixed bytes.
function indexOfBoundary(buf, from) {
  for (let i = from; i + 3 < buf.length; i++) {
    if (buf[i] === 13 && buf[i + 1] === 10 && buf[i + 2] === 13 && buf[i + 3] === 10) {
      return i;
    }
  }
  // Generic fallback in case the from-index skipped past it (shouldn't happen).
  outer: for (let i = 0; i + 3 < buf.length; i++) {
    for (let j = 0; j < HEADER_TERMINATOR.length; j++) {
      if (buf[i + j] !== HEADER_TERMINATOR[j]) continue outer;
    }
    return i;
  }
  return -1;
}

function parseRequestLine(headerStr) {
  const firstNl = headerStr.indexOf("\r\n");
  const first = firstNl < 0 ? headerStr : headerStr.substring(0, firstNl);
  const sp1 = first.indexOf(" ");
  const sp2 = first.indexOf(" ", sp1 + 1);
  const method = first.substring(0, sp1);
  const path = sp2 > 0 ? first.substring(sp1 + 1, sp2) : first.substring(sp1 + 1);

  const headers = new Headers();
  let contentLength = 0;
  let pos = firstNl + 2;
  while (pos < headerStr.length) {
    const nl = headerStr.indexOf("\r\n", pos);
    if (nl < 0 || nl === pos) break;
    const colon = headerStr.indexOf(":", pos);
    if (colon > 0 && colon < nl) {
      const k = headerStr.substring(pos, colon).trim();
      const v = headerStr.substring(colon + 1, nl).trim();
      headers.append(k, v);
      // content-length lookup is hot; do it inline once.
      if (contentLength === 0 && k.length === 14 &&
          (k === "Content-Length" || k.toLowerCase() === "content-length")) {
        contentLength = parseInt(v, 10) || 0;
      }
    }
    pos = nl + 2;
  }
  return { method, path, headers, contentLength };
}

// --- Reader adapter for WS messages. Same surface as ReadableStreamReader
// (read() → {value, done}) so the header-read code can be shared with both
// modes. ---
function makeWsReader(server) {
  const r = {
    queue: [],
    waiters: [],
    done: false,
    push(chunk) {
      // Normalise to Uint8Array. CF runtime sends ArrayBuffer for binary
      // frames and string for text frames.
      let u8;
      if (chunk instanceof Uint8Array) u8 = chunk;
      else if (chunk instanceof ArrayBuffer) u8 = new Uint8Array(chunk);
      else if (typeof chunk === "string") u8 = ENC.encode(chunk);
      else u8 = new Uint8Array(chunk);
      if (this.waiters.length) {
        this.waiters.shift()({ value: u8, done: false });
      } else {
        this.queue.push(u8);
      }
    },
    finish() {
      this.done = true;
      while (this.waiters.length) this.waiters.shift()({ value: undefined, done: true });
    },
    read() {
      if (this.queue.length) return Promise.resolve({ value: this.queue.shift(), done: false });
      if (this.done) return Promise.resolve({ value: undefined, done: true });
      return new Promise(res => this.waiters.push(res));
    },
  };
  server.addEventListener("message", (ev) => r.push(ev.data));
  server.addEventListener("close", () => r.finish());
  return r;
}

// --- Frame batching helper. ---
//
// Coalesces multiple small writes into a single WS frame to reduce
// per-frame overhead (header + length encoding, per-frame TCP send on the
// CF side). Flushes when:
//   - buffered size >= FLUSH_BYTES (16 KB), or
//   - flush() is called explicitly, or
//   - microtask queue drains and FLUSH_DELAY_MS has elapsed.
//
// Trade-off: adds at most ~1 ms of latency on slow streams to coalesce
// chunks. Net win for high-throughput downloads (fewer WS frames).
const FLUSH_BYTES = 16 * 1024;
const FLUSH_DELAY_MS = 1;

function makeFrameBatcher(server) {
  let buf = [];
  let bytes = 0;
  let flushTimer = null;

  function doFlush() {
    if (flushTimer !== null) { clearTimeout(flushTimer); flushTimer = null; }
    if (bytes === 0) return;
    if (buf.length === 1) {
      try { server.send(buf[0]); } catch {}
    } else {
      const out = new Uint8Array(bytes);
      let off = 0;
      for (const c of buf) { out.set(c, off); off += c.length; }
      try { server.send(out); } catch {}
    }
    buf = [];
    bytes = 0;
  }

  return {
    write(chunk) {
      const u8 = chunk instanceof Uint8Array ? chunk : new Uint8Array(chunk);
      // Big chunks bypass the batcher entirely — flushing first preserves
      // ordering, then we send the big chunk directly. Cheaper than
      // accumulating and re-allocing into a giant buffer.
      if (u8.length >= FLUSH_BYTES) {
        doFlush();
        try { server.send(u8); } catch {}
        return;
      }
      buf.push(u8);
      bytes += u8.length;
      if (bytes >= FLUSH_BYTES) {
        doFlush();
      } else if (flushTimer === null) {
        flushTimer = setTimeout(doFlush, FLUSH_DELAY_MS);
      }
    },
    flush: doFlush,
  };
}

// --- fetch mode: receive an HTTP request over WS, fetch() the target,
// stream the response back over WS. Used for CF-hosted targets (socket
// dial would 4001) and for plain HTTP traffic when the client wants to
// route through the Worker's fetch() rather than raw TCP. ---
async function handleFetchMode(server, host, port, tls) {
  // Send the probe byte the proxy expects; lets us reuse the same
  // post-WS-upgrade handshake regardless of mode.
  try { server.send(PROBE_BYTE); } catch {}

  const wsReader = makeWsReader(server);
  const out = makeFrameBatcher(server);

  try {
    const { headerBytes, leftover } = await readUntilHeadersEnd(wsReader, 64 * 1024);
    const { method, path, headers, contentLength } = parseRequestLine(DEC.decode(headerBytes));

    let body = null;
    if (contentLength > 0) {
      let acc = leftover;
      while (acc.length < contentLength) {
        const { value, done } = await wsReader.read();
        if (done) break;
        const merged = new Uint8Array(acc.length + value.length);
        merged.set(acc); merged.set(value, acc.length);
        acc = merged;
      }
      body = acc.subarray(0, contentLength);
    }

    const scheme = tls ? "https" : "http";
    const portSuffix = (tls && port === 443) || (!tls && port === 80) ? "" : `:${port}`;
    const url = `${scheme}://${host}${portSuffix}${path}`;

    headers.delete("host");
    headers.set("host", host);
    headers.delete("cf-connecting-ip");
    headers.delete("cf-ray");

    const upstream = await fetch(url, { method, headers, body, redirect: "manual" });

    let outHdr = `HTTP/1.1 ${upstream.status} ${upstream.statusText || ""}\r\n`;
    upstream.headers.forEach((v, k) => {
      // STRIP_RESPONSE_HEADERS is a Set, lookup is O(1) and lowercased.
      // toLowerCase() allocs but the Set check after dominates the cost
      // either way; the saved bytes downstream are worth it.
      if (STRIP_RESPONSE_HEADERS.has(k.toLowerCase())) return;
      outHdr += `${k}: ${v}\r\n`;
    });
    outHdr += "Connection: close\r\n\r\n";
    out.write(ENC.encode(outHdr));

    if (upstream.body) {
      const reader = upstream.body.getReader();
      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        out.write(value);
      }
    }
    out.flush();
  } catch (e) {
    out.flush();
    try {
      server.send(ENC.encode(`HTTP/1.1 502 ${e.message || "fetch error"}\r\nContent-Length: 0\r\n\r\n`));
    } catch {}
  } finally {
    try { server.close(1000, "fetch done"); } catch {}
  }
}

// --- socket mode: open a raw TCP socket via cloudflare:sockets and bridge
// it bidirectionally with the WebSocket. Hot path for non-CF targets;
// preserves arbitrary protocols (SSH, raw TLS, IRC, …). ---
async function handleSocketMode(server, host, port) {
  let socket;
  try {
    socket = connect({ hostname: host, port, secureTransport: "off", allowHalfOpen: false });
  } catch (e) {
    server.close(1011, "connect: " + e.message);
    return;
  }
  try {
    await socket.opened;
  } catch (e) {
    // Custom 4001 close code is the protocol marker FlareX uses to detect
    // CF-blocks-CF and trigger the byte-sniff → fetch fallback.
    server.close(4001, "upstream unreachable");
    return;
  }
  try { server.send(PROBE_BYTE); } catch {}

  const writer = socket.writable.getWriter();
  const out = makeFrameBatcher(server);

  // Client → upstream. Each WS frame becomes one socket write. Could be
  // batched too but the typical SOCKS5 flow sends one big request burst
  // followed by mostly target-driven traffic.
  server.addEventListener("message", async (ev) => {
    try {
      let buf;
      if (ev.data instanceof Uint8Array) buf = ev.data;
      else if (ev.data instanceof ArrayBuffer) buf = new Uint8Array(ev.data);
      else if (typeof ev.data === "string") buf = ENC.encode(ev.data);
      else buf = ev.data;
      await writer.write(buf);
    } catch {
      try { server.close(1011, "write fail"); } catch {}
    }
  });

  const closeBoth = (code, reason) => {
    out.flush();
    try { server.close(code, reason); } catch {}
    try { writer.close(); } catch {}
    try { socket.close(); } catch {}
  };

  server.addEventListener("close", () => closeBoth(1000, "client closed"));
  server.addEventListener("error", () => closeBoth(1011, "ws error"));

  // Upstream → client, batched.
  (async () => {
    const reader = socket.readable.getReader();
    try {
      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        out.write(value);
      }
    } catch {
      // upstream EOF or error — clean shutdown via closeBoth below.
    } finally {
      try { reader.releaseLock(); } catch {}
      closeBoth(1000, "upstream closed");
    }
  })();

  socket.closed.then(() => closeBoth(1000, "socket closed")).catch(() => closeBoth(1011, "socket err"));
}

export default {
  async fetch(req) {
    const url = new URL(req.url);

    if (url.pathname === "/__health") {
      const colo = (req.cf && req.cf.colo) ? String(req.cf.colo) : "";
      return new Response("ok", {
        status: 200,
        headers: {
          "Cache-Control": "no-store",
          "X-Template-Hash": "__TEMPLATE_HASH__",
          "X-CF-Colo": colo,
        },
      });
    }

    if (req.headers.get("Upgrade") !== "websocket") {
      return bad(426, "upgrade required");
    }

    const params = url.searchParams;
    const host = params.get("h");
    const port = parseInt(params.get("p") || "0", 10);
    const ts   = parseInt(params.get("ts") || "0", 10);
    const sig  = params.get("s") || "";
    const tls  = params.get("t") === "1";
    const mode = params.get("m") || "socket";

    if (!host || !port || !ts || !sig) return bad(400, "missing params");

    const now = Math.floor(Date.now() / 1000);
    if (Math.abs(now - ts) > TS_WINDOW_SEC) return bad(401, "ts expired");

    // Constant-time HMAC verify via WebCrypto. Avoids the timing-attack
    // surface of a JS string compare AND skips hex-encoding our own MAC.
    const sigBytes = hex2bytes(sig);
    if (!sigBytes) return bad(403, "bad sig encoding");
    const ok = await crypto.subtle.verify(
      "HMAC",
      await hmacKey(),
      sigBytes,
      ENC.encode(`${ts}|${host}|${port}|${tls ? 1 : 0}|${mode}`),
    );
    if (!ok) return bad(403, "bad sig");

    const pair = new WebSocketPair();
    const [client, server] = Object.values(pair);
    server.accept();

    if (mode === "fetch") {
      handleFetchMode(server, host, port, tls);
    } else {
      handleSocketMode(server, host, port);
    }

    return new Response(null, { status: 101, webSocket: client });
  }
};
