import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Activity, Copy, ExternalLink, RefreshCw, Terminal, X } from "lucide-react";
import { get, post, type Worker } from "../lib/api";
import { copy } from "../lib/clipboard";
import { workerDashboardURL } from "../lib/cfdash";
import { acctLabel, fmtAge, truncAcct } from "../lib/utils";
import { useConfirmDialog } from "./Dialog";

type Props = {
  worker: Worker | null;
  onClose: () => void;
  onAction: () => void;
  accountNames?: Record<string, string>;
};

// Derived from /config — extract the bits we need to build a correct curl
// snippet (listen addr + whether auth is required).
type ProxyInfo = {
  socksAddr: string;       // host:port (no scheme)
  httpAddr: string;        // host:port for HTTP CONNECT, empty if disabled
  authUser: string;        // "" when no auth configured
};

function parseProxyInfo(cfg: any): ProxyInfo {
  const listen = cfg?.listen ?? {};
  const stripScheme = (s: string) => String(s || "").replace(/^tcp:\/\//, "").replace(/^unix:\/\//, "");
  return {
    socksAddr: stripScheme(listen.socks5) || "127.0.0.1:1080",
    httpAddr: stripScheme(listen.http),
    authUser: typeof listen.auth_user === "string" ? listen.auth_user : "",
  };
}

export default function WorkerDetailDrawer({ worker, onClose, onAction, accountNames }: Props) {
  const confirm = useConfirmDialog();
  const [proxy, setProxy] = useState<ProxyInfo>({ socksAddr: "127.0.0.1:1080", httpAddr: "", authUser: "" });

  useEffect(() => {
    if (!worker) return;
    get<any>("/config").then((c) => setProxy(parseProxyInfo(c))).catch(() => {});
    function onKey(e: KeyboardEvent) { if (e.key === "Escape") onClose(); }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [worker, onClose]);

  if (!worker) return null;

  const w = worker;
  const dashURL = workerDashboardURL(w.account, w.name);
  const acctLabelStr = accountNames?.[w.account] || truncAcct(w.account);
  // Build a copy-ready curl snippet. If SOCKS auth is configured, inject a
  // $PASS placeholder the user substitutes from config.yaml or their
  // password manager (we never expose the pass via /config).
  const socksAuth = proxy.authUser ? `${proxy.authUser}:$PASS@` : "";
  const curlSnippet = `curl -x socks5h://${socksAuth}${proxy.socksAddr} https://ifconfig.me`;
  const probeSnippet = `curl -fsS "${w.url}/__health"`;

  async function recycle() {
    const ok = await confirm({
      title: `Recycle ${w.name}?`,
      description: "Drains inflight traffic, deletes the Worker, and redeploys a replacement with a fresh egress IP. Takes ~5–10 s.",
      confirmLabel: "Recycle",
    });
    if (!ok) return;
    try {
      const r = await post<{ old: string; new: string }>(`/workers/${encodeURIComponent(w.name)}/recycle`);
      toast.success(`Recycled ${r.old} → ${r.new}`);
      onAction();
      onClose();
    } catch (e: unknown) { toast.error(e instanceof Error ? e.message : String(e)); }
  }

  return (
    <>
      <div className="fixed inset-0 z-40 bg-black/50" onClick={onClose} aria-hidden />
      <aside
        className="fixed right-0 top-14 bottom-0 z-50 flex w-full max-w-md flex-col border-l bg-card shadow-xl md:w-[28rem]"
        role="dialog"
        aria-modal="true"
        aria-labelledby="worker-drawer-title"
      >
        <header className="flex items-start justify-between gap-3 border-b p-4">
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <h2 id="worker-drawer-title" className="truncate font-mono text-sm font-semibold">{w.name}</h2>
              {w.quota_paused ? <span className="pill pill-warn">quota</span>
                : w.healthy ? <span className="pill pill-ok">healthy</span>
                : <span className="pill pill-err">down</span>}
            </div>
            <div className="mt-0.5 text-xs text-muted-foreground">account {acctLabelStr}</div>
          </div>
          <button onClick={onClose} className="btn-ghost px-2 py-1" aria-label="Close">
            <X className="h-4 w-4" />
          </button>
        </header>

        <div className="flex-1 overflow-auto p-4 space-y-5">
          <section>
            <h3 className="mb-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">URL</h3>
            <div className="flex items-center gap-2">
              <code className="flex-1 overflow-x-auto rounded border bg-background px-2 py-1 font-mono text-xs">{w.url}</code>
              <button onClick={() => copy(w.url, "URL")} className="btn-ghost px-2 py-1" title="Copy URL" aria-label="Copy URL">
                <Copy className="h-3.5 w-3.5" />
              </button>
              <a href={dashURL} target="_blank" rel="noreferrer" className="btn-ghost px-2 py-1" title="Open on Cloudflare dashboard">
                <ExternalLink className="h-3.5 w-3.5" />
              </a>
            </div>
          </section>

          <section>
            <h3 className="mb-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Live stats</h3>
            <dl className="grid grid-cols-2 gap-2 text-xs">
              <Field label="Breaker" value={<span className={`pill ${w.breaker === "open" ? "pill-err" : w.breaker === "half-open" ? "pill-warn" : "pill-neutral"}`}>{w.breaker}</span>} />
              <Field label="Colo" value={w.colo || "—"} />
              <Field label="Age" value={fmtAge(w.age_sec)} />
              <Field label="Inflight" value={w.inflight} />
              <Field label="Requests" value={w.requests} />
              <Field label="Errors" value={w.errors} />
              <Field label="Err rate (EWMA)" value={`${(w.err_rate_ewma * 100).toFixed(1)}%`} />
              <Field label="Account" value={<div className="flex items-center gap-1">
                <span title={w.account}>{acctLabelStr}</span>
                <button onClick={() => copy(w.account, "account id")} className="rounded p-0.5 text-muted-foreground hover:bg-muted hover:text-foreground">
                  <Copy className="h-3 w-3" />
                </button>
              </div>} />
            </dl>
          </section>

          <section>
            <h3 className="mb-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Snippets</h3>
            <div className="space-y-2">
              <SnippetBox label="Proxy a request through the pool" value={curlSnippet} />
              <SnippetBox label="Probe this Worker directly" value={probeSnippet} />
            </div>
            <p className="mt-1.5 text-[11px] text-muted-foreground">
              {proxy.authUser
                ? <>Listener requires RFC-1929 auth. Replace <code>$PASS</code> with <code>listen.auth_pass</code> from your config.</>
                : <>No SOCKS auth configured — loopback only. Never expose <code>listen.socks5</code> past 127.0.0.1 without credentials.</>}
            </p>
          </section>
        </div>

        <footer className="flex gap-2 border-t p-3">
          <button onClick={recycle} className="btn-accent flex-1 text-xs">
            <RefreshCw className="h-3.5 w-3.5" /> Recycle
          </button>
          <a href={dashURL} target="_blank" rel="noreferrer" className="btn-ghost flex-1 text-xs">
            <ExternalLink className="h-3.5 w-3.5" /> CF dashboard
          </a>
          <a href={`#/logs?worker=${encodeURIComponent(w.name)}`} className="btn-ghost text-xs" onClick={onClose} title="Tail Cloudflare logs for this Worker">
            <Activity className="h-3.5 w-3.5" /> Logs
          </a>
        </footer>
      </aside>
    </>
  );
}

function Field({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="rounded border bg-background/60 px-2 py-1.5">
      <div className="text-[10px] uppercase tracking-wider text-muted-foreground">{label}</div>
      <div className="mt-0.5 font-mono text-xs">{value}</div>
    </div>
  );
}

function SnippetBox({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="mb-1 flex items-center gap-1 text-[11px] text-muted-foreground">
        <Terminal className="h-3 w-3" /> {label}
      </div>
      <div className="flex items-center gap-1">
        <code className="flex-1 overflow-x-auto rounded border bg-background px-2 py-1 font-mono text-[11px]">{value}</code>
        <button onClick={() => copy(value, "snippet")} className="btn-ghost px-2 py-1" title="Copy" aria-label="Copy snippet">
          <Copy className="h-3 w-3" />
        </button>
      </div>
    </div>
  );
}

