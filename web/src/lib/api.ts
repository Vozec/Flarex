// Thin fetch wrapper that rides on the cookie session set by
// lib/auth.ts::login(). Mutations echo the CSRF token via the header
// (double-submit cookie). Errors surface as ApiError so pages can toast.

import { authHeaders } from "./auth";

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

async function req(method: string, path: string, body?: unknown): Promise<Response> {
  const headers: Record<string, string> = { ...(authHeaders(method) as Record<string, string>) };
  const opts: RequestInit = { method, credentials: "include", headers };
  if (body !== undefined) {
    headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  const r = await fetch(path, opts);
  if (!r.ok) {
    const text = await r.text().catch(() => "");
    throw new ApiError(r.status, text || `HTTP ${r.status}`);
  }
  return r;
}

export async function get<T = unknown>(path: string): Promise<T> {
  const r = await req("GET", path);
  return (await r.json()) as T;
}
export async function post<T = unknown>(path: string, body?: unknown): Promise<T> {
  const r = await req("POST", path, body);
  return (await r.json()) as T;
}
export async function del<T = unknown>(path: string): Promise<T> {
  const r = await req("DELETE", path);
  return (await r.json()) as T;
}
export async function patch<T = unknown>(path: string, body: unknown): Promise<T> {
  const r = await req("PATCH", path, body);
  return (await r.json()) as T;
}

// stream returns the raw Response — used by the Logs tab for SSE.
export async function stream(path: string, signal?: AbortSignal): Promise<Response> {
  const r = await fetch(path, { credentials: "include", signal });
  if (!r.ok) throw new ApiError(r.status, `HTTP ${r.status}`);
  return r;
}

// --- typed views ---

export type Worker = {
  name: string;
  url: string;
  account: string;
  healthy: boolean;
  quota_paused: boolean;
  breaker: string;
  inflight: number;
  requests: number;
  errors: number;
  err_rate_ewma: number;
  age_sec: number;
  colo?: string;
};
export type StatusResp = { pool_size: number; workers: Worker[] };

export type Account = {
  id: string;
  name?: string;
  workers: number;
  healthy: number;
  quota_paused: number;
};
export type AccountsResp = { accounts: Account[] };

export type APIKey = {
  id: string;
  name: string;
  prefix: string;
  scopes: string[];
  disabled: boolean;
  expired?: boolean;
  created_at: string;
  last_used_at?: string;
  expires_at?: string | null;
};
export type APIKeysResp = { keys: APIKey[] };

export type CreateAPIKeyResp = {
  id: string;
  name: string;
  prefix: string;
  scopes: string[];
  expires_at?: string | null;
  key: string;
  note: string;
};

export type MetricsSample = {
  at: string;
  connections: number;
  dial_success: number;
  dial_fail: number;
  handshake_fail: number;
  bytes_upstream: number;
  bytes_downstream: number;
  fetch_fallback: number;
  hedge_fired: number;
  hedge_wins: number;
  latency_p50_ms: number;
  latency_p95_ms: number;
  latency_p99_ms: number;
  worker_requests?: Record<string, number>;
};

export type QuotaDay = {
  date: string;
  account_id: string;
  used: number;
  limit: number;
};

export type AuditEvent = {
  at: string;
  who: string;
  action: string;
  target: string;
  detail?: string;
};
export type AuditResp = { events: AuditEvent[] };

export type TestRequestResult = {
  worker: string;
  colo?: string;
  egress_ip?: string;
  status: number;
  latency_ms: number;
  mode: string;
  headers?: Record<string, string>;
  body: string;
  body_trunc_at?: number;
  resolved_host?: string;
};
