// Cookie-based auth for the admin SPA.
//
// Flow:
//   1. User enters the api_key (bootstrap or a minted flx_* key) in the
//      LoginScreen → `login()` POSTs /session with JSON body.
//   2. Server validates, sets HttpOnly `flarex_session` + `flarex_csrf`
//      cookies, returns {who, expires_at, csrf_token}.
//   3. Every subsequent request is same-origin with `credentials:
//      "include"`. Mutations (POST/DELETE/PATCH) echo the CSRF token
//      back in `X-CSRF-Token` (double-submit cookie).
//   4. `logout()` DELETEs /session which clears both cookies.
//
// We don't store the raw api_key anywhere — the SPA only holds the
// short-lived cookie + CSRF token. That dramatically reduces the blast
// radius of an XSS in a dependency.

export type SessionInfo = {
  who: string;
  expires_at: number;
  csrf_token: string;
};

const CSRF_HEADER = "X-CSRF-Token";
let csrfToken: string | null = null;

export function currentCSRF(): string | null {
  return csrfToken;
}

// Read the CSRF cookie on boot — handles the page-reload case where the
// server already issued a session.
function readCSRFCookie(): string {
  for (const c of document.cookie.split(";")) {
    const [k, v] = c.trim().split("=");
    if (k === "flarex_csrf") return v;
  }
  return "";
}

// TOTPRequiredError is thrown by login() when the server requires a TOTP
// code but none (or a wrong one) was supplied. LoginScreen catches this
// to render the 6-digit input. Any other error is an auth failure.
export class TOTPRequiredError extends Error {
  constructor(public wrong: boolean) {
    super(wrong ? "invalid TOTP code" : "TOTP code required");
  }
}

export async function login(apiKey: string, totpCode?: string): Promise<SessionInfo> {
  const r = await fetch("/session", {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ api_key: apiKey, totp_code: totpCode || "" }),
  });
  if (!r.ok) {
    const ct = r.headers.get("content-type") || "";
    if (r.status === 401 && ct.includes("application/json")) {
      const body = await r.json().catch(() => ({}));
      if (body?.totp_required) {
        throw new TOTPRequiredError(Boolean(totpCode));
      }
    }
    const t = await r.text().catch(() => "");
    throw new Error(t || `HTTP ${r.status}`);
  }
  const info = (await r.json()) as SessionInfo;
  csrfToken = info.csrf_token;
  return info;
}

export async function logout(): Promise<void> {
  csrfToken = null;
  try {
    await fetch("/session", { method: "DELETE", credentials: "include" });
  } catch {
    // ignore
  }
}

// tryBootstrapSession probes /status with the current cookies. Returns
// session metadata when authenticated, null when no valid session.
export async function tryBootstrapSession(): Promise<SessionInfo | null> {
  const cookie = readCSRFCookie();
  if (!cookie) return null;
  const r = await fetch("/status", {
    method: "GET",
    credentials: "include",
    headers: { "X-Requested-With": "flarex-ui" },
  }).catch(() => null);
  if (!r || !r.ok) return null;
  csrfToken = cookie;
  return { who: "session", expires_at: 0, csrf_token: cookie };
}

export function authHeaders(method: string): HeadersInit {
  const h: Record<string, string> = {};
  const isMutation = method !== "GET" && method !== "HEAD" && method !== "OPTIONS";
  if (isMutation && csrfToken) {
    h[CSRF_HEADER] = csrfToken;
  }
  return h;
}
