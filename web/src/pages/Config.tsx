import { useEffect, useMemo, useState } from "react";
import { Save, RotateCcw, AlertTriangle } from "lucide-react";
import { toast } from "sonner";
import { get } from "../lib/api";
import { authHeaders } from "../lib/auth";
import ErrorBanner from "../components/ErrorBanner";
import { usePageTitle } from "../lib/usePageTitle";

type FieldType = "string" | "password" | "int" | "uint64" | "bool" | "select" | "ports" | "cidrs" | "float";

type FieldDef = {
  path: string;            // dot-notation matching backend registry
  label: string;
  type: FieldType;
  options?: string[];      // for `select`
  help?: string;
  requiresRestart?: boolean;
};

type SectionDef = {
  title: string;
  note?: string;
  fields: FieldDef[];
};

// SECTIONS mirrors the Go-side applyConfigUpdate registry. Each field
// here MUST have a matching case in cmd/flarex/config_update.go — if not,
// the PATCH returns "unknown path". Keep the two in sync.
const SECTIONS: SectionDef[] = [
  {
    title: "Proxy behaviour",
    note: "Applied live — new dials pick up immediately.",
    fields: [
      { path: "pool.proxy_mode", label: "Proxy mode", type: "select", options: ["hybrid", "socket", "fetch"], help: "hybrid=smart fallback, socket=raw TCP, fetch=HTTP via Worker fetch()" },
      { path: "pool.strategy", label: "Scheduling strategy", type: "select", options: ["round_robin", "least_inflight"], requiresRestart: true },
      { path: "pool.max_retries", label: "Max retries", type: "int" },
      { path: "pool.backoff_ms", label: "Retry backoff (ms)", type: "int" },
      { path: "pool.hedge_after_ms", label: "Hedge after (ms)", type: "int", help: "0 = off; N = fire a 2nd worker if primary dial takes > N ms" },
      { path: "pool.tls_rewrap", label: "TLS rewrap (uTLS)", type: "bool", help: "Wrap conn with uTLS random fingerprint" },
      { path: "pool.disable_probe", label: "Disable probe byte", type: "bool", requiresRestart: true, help: "Skip the initial probe byte on each dial (max throughput)" },
    ],
  },
  {
    title: "Filter",
    note: "Port allowlist + deny CIDRs applied per dial.",
    fields: [
      { path: "filter.allow_ports", label: "Allowed ports", type: "ports", help: "Comma-separated list. e.g. 22, 80, 443, 3306" },
      { path: "filter.deny_cidrs", label: "Extra deny CIDRs", type: "cidrs", requiresRestart: true, help: "Added on top of always-on SSRF defaults" },
    ],
  },
  {
    title: "Rate limit",
    fields: [
      { path: "rate_limit.per_host_qps", label: "Per-host QPS", type: "float", requiresRestart: true },
      { path: "rate_limit.per_host_burst", label: "Per-host burst", type: "int", requiresRestart: true },
    ],
  },
  {
    title: "Worker",
    note: "Affects next deploy — existing pool is unchanged.",
    fields: [
      { path: "worker.count", label: "Workers per account", type: "int" },
      { path: "worker.name_prefix", label: "Name prefix", type: "string", help: "Guards `flarex clean` scope — never leave empty" },
      { path: "worker.deploy_backend", label: "Deploy backend", type: "select", options: ["workers_dev", "custom_domain", "auto"] },
      { path: "worker.rotate_interval_sec", label: "Rotation check interval (sec)", type: "int", requiresRestart: true },
      { path: "worker.rotate_max_age_sec", label: "Recycle after age (sec)", type: "int", requiresRestart: true, help: "0 = off" },
      { path: "worker.rotate_max_req", label: "Recycle after N requests", type: "uint64", requiresRestart: true, help: "0 = off" },
    ],
  },
  {
    title: "Listener",
    note: "Auth creds are applied at startup — change takes effect after restart.",
    fields: [
      { path: "listen.auth_user", label: "SOCKS / HTTP auth user", type: "string", requiresRestart: true },
      { path: "listen.auth_pass", label: "SOCKS / HTTP auth password", type: "password", requiresRestart: true },
    ],
  },
  {
    title: "Admin",
    fields: [
      { path: "admin.api_key", label: "Admin bootstrap API key", type: "password", requiresRestart: true, help: "Required for CLI + as fallback login" },
      { path: "admin.enable_pprof", label: "Enable /debug/pprof/*", type: "bool", requiresRestart: true },
    ],
  },
  {
    title: "Quota & alerts",
    fields: [
      { path: "quota.daily_limit", label: "Daily subrequest limit", type: "uint64", requiresRestart: true },
      { path: "quota.warn_percent", label: "Warn threshold (%)", type: "int" },
      { path: "alerts.cooldown_sec", label: "Alert cooldown (sec)", type: "int", requiresRestart: true },
      { path: "alerts.discord_webhook_url", label: "Discord webhook URL", type: "string", requiresRestart: true },
    ],
  },
  {
    title: "Logging",
    fields: [
      { path: "log.level", label: "Log level", type: "select", options: ["trace", "debug", "info", "warn", "error"], requiresRestart: true },
    ],
  },
];

// Extract current value from the config dump using dot-notation. Returns
// `undefined` when the path doesn't exist yet (fresh field).
function getAt(cfg: any, path: string): any {
  let cur = cfg;
  for (const seg of path.split(".")) {
    if (cur == null) return undefined;
    cur = cur[seg];
  }
  return cur;
}

// Serialize current value into the shape the <input> expects.
function toInput(type: FieldType, v: any): string {
  if (v == null) return "";
  if (type === "ports" || type === "cidrs") {
    if (Array.isArray(v)) return v.join(", ");
    return String(v);
  }
  if (type === "bool") return v ? "true" : "false";
  return String(v);
}

// Parse the input string back into the JSON value the PATCH expects.
function fromInput(type: FieldType, s: string): any {
  switch (type) {
    case "int":
    case "uint64":
      return s === "" ? 0 : Number(s);
    case "float":
      return s === "" ? 0 : Number(s);
    case "bool":
      return s === "true";
    case "ports":
      return s.split(",").map((p) => p.trim()).filter(Boolean).map((p) => Number(p));
    case "cidrs":
      return s.split(",").map((p) => p.trim()).filter(Boolean);
    default:
      return s;
  }
}

async function patchConfig(path: string, value: any): Promise<{ applied: boolean; requires_restart: boolean }> {
  const r = await fetch("/config", {
    method: "PATCH",
    credentials: "include",
    headers: { "Content-Type": "application/json", ...(authHeaders("PATCH") as Record<string, string>) },
    body: JSON.stringify({ path, value }),
  });
  if (!r.ok) {
    const t = await r.text().catch(() => "");
    try { const j = JSON.parse(t); throw new Error(j.error || t); }
    catch { throw new Error(t || `HTTP ${r.status}`); }
  }
  return r.json();
}

export default function ConfigPage() {
  usePageTitle("Config");
  const [cfg, setCfg] = useState<any>(null);
  const [err, setErr] = useState<unknown>(null);
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [saving, setSaving] = useState<string | null>(null);
  const [showRaw, setShowRaw] = useState(false);

  async function load() {
    try {
      const c = await get<any>("/config");
      setCfg(c);
      const next: Record<string, string> = {};
      for (const sec of SECTIONS) {
        for (const f of sec.fields) {
          next[f.path] = toInput(f.type, getAt(c, f.path));
        }
      }
      setDrafts(next);
      setErr(null);
    } catch (e) { setErr(e); }
  }
  useEffect(() => { load(); }, []);

  const dirty = useMemo(() => {
    if (!cfg) return new Set<string>();
    const d = new Set<string>();
    for (const sec of SECTIONS) {
      for (const f of sec.fields) {
        const saved = toInput(f.type, getAt(cfg, f.path));
        if ((drafts[f.path] ?? "") !== saved) d.add(f.path);
      }
    }
    return d;
  }, [cfg, drafts]);

  async function save(field: FieldDef) {
    setSaving(field.path);
    try {
      const value = fromInput(field.type, drafts[field.path] ?? "");
      const res = await patchConfig(field.path, value);
      toast.success(`${field.label} updated${res.requires_restart ? " — restart required" : ""}`);
      await load();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(null);
    }
  }
  function revert(field: FieldDef) {
    setDrafts((d) => ({ ...d, [field.path]: toInput(field.type, getAt(cfg, field.path)) }));
  }

  return (
    <div className="space-y-6">
      <ErrorBanner error={err} />

      {SECTIONS.map((sec) => (
        <section key={sec.title} className="card p-5">
          <div className="mb-4 flex items-start justify-between gap-3">
            <div>
              <h2 className="text-sm font-semibold">{sec.title}</h2>
              {sec.note && <p className="mt-0.5 text-xs text-muted-foreground">{sec.note}</p>}
            </div>
          </div>

          <div className="grid gap-4 md:grid-cols-2">
            {sec.fields.map((f) => {
              const draft = drafts[f.path] ?? "";
              const isDirty = dirty.has(f.path);
              const isSaving = saving === f.path;
              return (
                <div key={f.path} className="space-y-1.5">
                  <label className="flex items-center gap-2 text-xs font-medium">
                    {f.label}
                    {f.requiresRestart && (
                      <span className="inline-flex items-center gap-1 text-[10px] text-amber-500" title="Updates cfg but needs restart to take effect">
                        <AlertTriangle className="h-3 w-3" /> restart
                      </span>
                    )}
                  </label>

                  <div className="flex gap-2">
                    {f.type === "select" && (
                      <select
                        value={draft}
                        onChange={(e) => setDrafts((d) => ({ ...d, [f.path]: e.target.value }))}
                        className="input flex-1"
                      >
                        {f.options!.map((o) => <option key={o} value={o}>{o}</option>)}
                      </select>
                    )}
                    {f.type === "bool" && (
                      <select
                        value={draft}
                        onChange={(e) => setDrafts((d) => ({ ...d, [f.path]: e.target.value }))}
                        className="input flex-1"
                      >
                        <option value="true">true</option>
                        <option value="false">false</option>
                      </select>
                    )}
                    {(f.type === "string" || f.type === "password" || f.type === "ports" || f.type === "cidrs") && (
                      <input
                        type={f.type === "password" ? "password" : "text"}
                        value={draft}
                        onChange={(e) => setDrafts((d) => ({ ...d, [f.path]: e.target.value }))}
                        className="input flex-1 font-mono"
                        placeholder={f.type === "ports" ? "22, 80, 443" : f.type === "cidrs" ? "10.0.0.0/8, 192.168.0.0/16" : ""}
                      />
                    )}
                    {(f.type === "int" || f.type === "uint64" || f.type === "float") && (
                      <input
                        type="number"
                        step={f.type === "float" ? "any" : 1}
                        value={draft}
                        onChange={(e) => setDrafts((d) => ({ ...d, [f.path]: e.target.value }))}
                        className="input flex-1 font-mono"
                      />
                    )}

                    {isDirty && (
                      <>
                        <button
                          onClick={() => save(f)}
                          disabled={isSaving}
                          className="btn-accent text-xs"
                          title="Apply change"
                        >
                          <Save className="h-3.5 w-3.5" /> {isSaving ? "…" : "Save"}
                        </button>
                        <button
                          onClick={() => revert(f)}
                          className="btn-ghost text-xs"
                          title="Revert"
                        >
                          <RotateCcw className="h-3.5 w-3.5" />
                        </button>
                      </>
                    )}
                  </div>

                  {f.help && <p className="text-[11px] text-muted-foreground">{f.help}</p>}
                </div>
              );
            })}
          </div>
        </section>
      ))}

      <section className="card p-4">
        <div className="mb-2 flex items-center justify-between">
          <h2 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            Raw running config (secrets redacted)
          </h2>
          <button onClick={() => setShowRaw((s) => !s)} className="btn-ghost text-xs">
            {showRaw ? "Hide" : "Show"}
          </button>
        </div>
        {showRaw && (
          <pre className="overflow-auto rounded bg-muted/40 p-4 text-xs leading-relaxed max-h-[50vh]">
            {cfg ? JSON.stringify(cfg, null, 2) : "loading…"}
          </pre>
        )}
      </section>
    </div>
  );
}
