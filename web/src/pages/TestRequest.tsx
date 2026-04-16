import { useEffect, useState } from "react";
import { Send, Copy as CopyIcon, Zap, Clock, MapPin, Boxes, History, RotateCcw } from "lucide-react";
import { toast } from "sonner";
import { get, post, type TestRequestResult } from "../lib/api";
import ErrorBanner from "../components/ErrorBanner";
import { copy } from "../lib/clipboard";
import { usePageTitle } from "../lib/usePageTitle";

const PRESETS = [
  "https://httpbin.org/get",
  "https://ifconfig.me",
  "https://api.ipify.org?format=json",
  "https://www.cloudflare.com/cdn-cgi/trace",
];

type HistoryEntry = { at: string; url: string; result: TestRequestResult };

export default function TestRequest() {
  usePageTitle("Test request");
  const [url, setUrl] = useState("https://httpbin.org/get");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<unknown>(null);
  const [res, setRes] = useState<TestRequestResult | null>(null);
  const [history, setHistory] = useState<HistoryEntry[]>([]);

  async function loadHistory() {
    try {
      const r = await get<{ runs: HistoryEntry[] }>("/test-history");
      setHistory(r.runs || []);
    } catch { /* ignore */ }
  }
  useEffect(() => { loadHistory(); }, []);

  async function run(e?: React.FormEvent, overrideURL?: string) {
    e?.preventDefault();
    const target = overrideURL || url;
    if (overrideURL) setUrl(overrideURL);
    setBusy(true);
    setErr(null);
    try {
      const r = await post<TestRequestResult>("/test-request", { url: target });
      setRes(r);
      toast.success(`${r.status} via ${r.worker} (${r.latency_ms} ms)`);
      await loadHistory();
    } catch (e) {
      setErr(e);
      setRes(null);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-4">
      <ErrorBanner error={err} />

      <section className="card p-4">
        <h2 className="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          Send a request through the pool
        </h2>
        <form onSubmit={run} className="flex flex-wrap gap-2">
          <input
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            placeholder="https://example.com/..."
            className="input flex-1 font-mono min-w-80"
          />
          <button type="submit" className="btn-accent" disabled={busy || !url}>
            <Send className="h-4 w-4" />
            {busy ? "Sending…" : "Send"}
          </button>
        </form>
        <div className="mt-3 flex flex-wrap gap-1.5 text-xs">
          <span className="text-muted-foreground">Presets:</span>
          {PRESETS.map((p) => (
            <button
              key={p}
              type="button"
              onClick={() => setUrl(p)}
              className="rounded border border-border px-2 py-0.5 font-mono text-[11px] hover:bg-muted"
            >
              {p}
            </button>
          ))}
        </div>
        <p className="mt-2 text-xs text-muted-foreground">
          Uses the same dial policy as live SOCKS5/CONNECT traffic: current
          proxy mode, hedge-after, breakers, sticky sessions off. Body is
          capped at 4 KB.
        </p>
      </section>

      {res && (
        <section className="card p-4 space-y-4">
          <div className="grid gap-3 sm:grid-cols-2 md:grid-cols-4">
            <Tile icon={<Boxes className="h-4 w-4" />} label="Worker" value={res.worker} mono copy />
            <Tile icon={<MapPin className="h-4 w-4" />} label="Colo" value={res.colo || "—"} mono />
            <Tile icon={<Zap className="h-4 w-4" />} label="Mode" value={res.mode} mono />
            <Tile icon={<Clock className="h-4 w-4" />} label="Latency" value={`${res.latency_ms} ms`} />
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <Tile label="HTTP status" value={<StatusPill code={res.status} />} />
            <Tile label="Egress IP" value={res.egress_ip || "—"} mono copy />
          </div>

          <div>
            <h3 className="mb-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
              Response headers
            </h3>
            <div className="max-h-52 overflow-auto rounded border">
              <table className="w-full text-xs">
                <tbody className="font-mono">
                  {Object.entries(res.headers || {}).map(([k, v]) => (
                    <tr key={k} className="border-t first:border-0">
                      <td className="whitespace-nowrap bg-muted/30 px-2 py-1 text-muted-foreground">{k}</td>
                      <td className="break-all px-2 py-1">{v}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>

          <div>
            <div className="mb-2 flex items-center justify-between">
              <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                Body
                {res.body_trunc_at ? (
                  <span className="ml-2 text-amber-500">(truncated at {res.body_trunc_at} B)</span>
                ) : null}
              </h3>
              {res.body && (
                <button
                  onClick={() => copy(res.body, "body")}
                  className="btn-ghost text-xs"
                  title="Copy body"
                >
                  <CopyIcon className="h-3 w-3" /> Copy
                </button>
              )}
            </div>
            <pre className="max-h-80 overflow-auto rounded border bg-background p-2 font-mono text-[11px] leading-relaxed whitespace-pre-wrap">
              {res.body || "(empty)"}
            </pre>
          </div>
        </section>
      )}

      {history.length > 0 && (
        <section className="card p-4">
          <h2 className="mb-3 flex items-center gap-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            <History className="h-3.5 w-3.5" /> History ({history.length})
          </h2>
          <div className="overflow-auto rounded border">
            <table className="w-full text-xs">
              <thead className="bg-muted/50 text-[10px] uppercase tracking-wider text-muted-foreground">
                <tr>
                  <th className="px-2 py-1.5 text-left">When</th>
                  <th className="px-2 py-1.5 text-left">URL</th>
                  <th className="px-2 py-1.5 text-left">Worker</th>
                  <th className="px-2 py-1.5 text-left">Status</th>
                  <th className="px-2 py-1.5 text-right">Latency</th>
                  <th className="px-2 py-1.5 text-right"></th>
                </tr>
              </thead>
              <tbody className="font-mono">
                {history.map((h, i) => (
                  <tr key={i} className="border-t">
                    <td className="px-2 py-1.5 text-muted-foreground whitespace-nowrap">{new Date(h.at).toLocaleTimeString()}</td>
                    <td className="px-2 py-1.5 truncate max-w-[20rem]" title={h.url}>{h.url}</td>
                    <td className="px-2 py-1.5 text-muted-foreground">{h.result.worker}</td>
                    <td className="px-2 py-1.5">
                      <span className={`pill ${h.result.status >= 400 ? "pill-err" : "pill-ok"}`}>{h.result.status}</span>
                    </td>
                    <td className="px-2 py-1.5 text-right">{h.result.latency_ms} ms</td>
                    <td className="px-2 py-1.5 text-right">
                      <button
                        onClick={() => run(undefined, h.url)}
                        className="btn-ghost text-[11px]"
                        disabled={busy}
                        title="Replay this URL"
                      >
                        <RotateCcw className="h-3 w-3" /> replay
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      )}
    </div>
  );
}

function Tile({
  icon, label, value, mono, copy: canCopy,
}: {
  icon?: React.ReactNode;
  label: string;
  value: React.ReactNode;
  mono?: boolean;
  copy?: boolean;
}) {
  const str = typeof value === "string" ? value : "";
  return (
    <div className="rounded border bg-background/60 p-3">
      <div className="flex items-center gap-1 text-[10px] uppercase tracking-wider text-muted-foreground">
        {icon}
        {label}
      </div>
      <div className="mt-1 flex items-center gap-1">
        <div className={mono ? "font-mono text-sm" : "text-sm"}>{value}</div>
        {canCopy && str && (
          <button
            onClick={() => copy(str, label.toLowerCase())}
            className="rounded p-0.5 text-muted-foreground hover:bg-muted hover:text-foreground"
            aria-label={`Copy ${label}`}
          >
            <CopyIcon className="h-3 w-3" />
          </button>
        )}
      </div>
    </div>
  );
}

function StatusPill({ code }: { code: number }) {
  const tone = code >= 500 ? "pill-err" : code >= 400 ? "pill-warn" : code >= 200 ? "pill-ok" : "pill-neutral";
  return <span className={`pill ${tone}`}>{code}</span>;
}
