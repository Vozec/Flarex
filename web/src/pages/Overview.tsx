import { useEffect, useState } from "react";
import { Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { get, type AccountsResp, type MetricsSample, type QuotaDay, type StatusResp } from "../lib/api";
import Stat from "../components/Stat";
import ErrorBanner from "../components/ErrorBanner";
import { fmtBytes, truncAcct } from "../lib/utils";
import { usePageTitle } from "../lib/usePageTitle";
import ColoMap from "../components/ColoMap";

export default function Overview() {
  usePageTitle("Overview");
  const [status, setStatus] = useState<StatusResp | null>(null);
  const [samples, setSamples] = useState<MetricsSample[]>([]);
  const [quota, setQuota] = useState<QuotaDay[]>([]);
  const [accountNames, setAccountNames] = useState<Record<string, string>>({});
  const [err, setErr] = useState<unknown>(null);

  useEffect(() => {
    let alive = true;
    async function tick() {
      try {
        const [s, m, q, a] = await Promise.all([
          get<StatusResp>("/status"),
          get<{ samples: MetricsSample[] }>("/metrics/series").catch(() => ({ samples: [] })),
          get<{ series: QuotaDay[] }>("/metrics/history?days=1").catch(() => ({ series: [] })),
          get<AccountsResp>("/accounts").catch(() => ({ accounts: [] })),
        ]);
        if (!alive) return;
        setStatus(s);
        setSamples(m.samples || []);
        setQuota(q.series || []);
        const names: Record<string, string> = {};
        for (const acc of a.accounts || []) {
          if (acc.name) names[acc.id] = acc.name;
        }
        setAccountNames(names);
        setErr(null);
      } catch (e) {
        if (alive) setErr(e);
      }
    }
    tick();
    const h = setInterval(tick, 10_000);
    return () => { alive = false; clearInterval(h); };
  }, []);

  const healthy = status?.workers.filter((w) => w.healthy).length ?? 0;
  const paused = status?.workers.filter((w) => w.quota_paused).length ?? 0;
  const open = status?.workers.filter((w) => w.breaker === "open").length ?? 0;
  const accounts = new Set(status?.workers.map((w) => w.account)).size;

  // Per-colo distribution — bucket workers by their reported CF colo.
  const coloBuckets = (() => {
    const m = new Map<string, number>();
    for (const w of status?.workers || []) {
      const k = w.colo || "?";
      m.set(k, (m.get(k) || 0) + 1);
    }
    return Array.from(m.entries()).sort((a, b) => b[1] - a[1]);
  })();
  const coloMax = coloBuckets.reduce((acc, [, v]) => Math.max(acc, v), 1);
  const topWorkers = (() => {
    const arr = [...(status?.workers || [])];
    arr.sort((a, b) => b.requests - a.requests);
    return arr.slice(0, 5);
  })();
  const reqMax = topWorkers.reduce((acc, w) => Math.max(acc, w.requests), 1);

  // Per-second deltas from the two most recent ring samples.
  const last = samples[samples.length - 1];
  const prev = samples[samples.length - 2];
  const throughput = (() => {
    if (!last || !prev) return null;
    const dt = (new Date(last.at).getTime() - new Date(prev.at).getTime()) / 1000;
    if (dt <= 0) return null;
    return {
      cps: Math.max(0, (last.connections - prev.connections) / dt),
      upBps: Math.max(0, (last.bytes_upstream - prev.bytes_upstream) / dt),
      downBps: Math.max(0, (last.bytes_downstream - prev.bytes_downstream) / dt),
      handshakeFail: last.handshake_fail,
      totalBytes: last.bytes_upstream + last.bytes_downstream,
    };
  })();

  // Turn cumulative counters into per-minute rates for the chart.
  const rateData = samples.slice(1).map((s, i) => {
    const prev = samples[i];
    const dt = (new Date(s.at).getTime() - new Date(prev.at).getTime()) / 1000;
    const dreq = s.dial_success - prev.dial_success + (s.dial_fail - prev.dial_fail);
    return {
      at: new Date(s.at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }),
      rps: dt > 0 ? +(dreq / dt).toFixed(2) : 0,
      p50: +s.latency_p50_ms.toFixed(1),
      p95: +s.latency_p95_ms.toFixed(1),
    };
  });

  return (
    <div className="space-y-6">
      <ErrorBanner error={err} />
      <div className="grid gap-4 sm:grid-cols-2 md:grid-cols-5">
        <Stat label="Pool size" value={status?.pool_size ?? "–"} accent />
        <Stat label="Healthy" value={`${healthy}${status ? ` / ${status.pool_size}` : ""}`} />
        <Stat label="Quota paused" value={paused} />
        <Stat label="Breakers open" value={open} />
        <Stat label="Accounts" value={accounts} />
      </div>

      {throughput && (
        <div className="grid gap-4 sm:grid-cols-2 md:grid-cols-4">
          <Stat label="Conn / s" value={throughput.cps.toFixed(2)} />
          <Stat label="↑ bytes / s" value={fmtBytes(throughput.upBps) + "/s"} />
          <Stat label="↓ bytes / s" value={fmtBytes(throughput.downBps) + "/s"} />
          <Stat label="Handshake fails (total)" value={throughput.handshakeFail} />
        </div>
      )}

      {last && (
        <div className="grid gap-4 sm:grid-cols-2 md:grid-cols-3">
          <Stat label="Fetch fallbacks" value={last.fetch_fallback} />
          <Stat
            label="Hedged dials"
            value={
              last.hedge_fired === 0
                ? "0"
                : `${last.hedge_wins} / ${last.hedge_fired} (${((last.hedge_wins / last.hedge_fired) * 100).toFixed(0)}%)`
            }
          />
          <Stat label="Dial failures (total)" value={last.dial_fail} />
        </div>
      )}

      {quota.length > 0 && (
        <section className="card p-4">
          <h2 className="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            Quota today (per account)
          </h2>
          <div className="space-y-2">
            {quota.map((q) => {
              const limit = q.limit || 100_000;
              const pct = Math.min(100, (q.used / limit) * 100);
              const tone = pct >= 90 ? "bg-red-500" : pct >= 75 ? "bg-amber-500" : "bg-accent";
              const label = accountNames[q.account_id] || truncAcct(q.account_id);
              return (
                <div key={q.account_id} className="text-xs">
                  <div className="mb-1 flex items-center justify-between">
                    <span title={q.account_id} className="font-semibold">{label}</span>
                    <span className={pct >= 90 ? "text-red-500" : pct >= 75 ? "text-amber-500" : "text-muted-foreground"}>
                      {q.used.toLocaleString()} / {limit.toLocaleString()} · {pct.toFixed(1)}%
                    </span>
                  </div>
                  <div className="h-2 w-full overflow-hidden rounded bg-muted">
                    <div className={`h-full ${tone} transition-all`} style={{ width: `${pct}%` }} />
                  </div>
                </div>
              );
            })}
          </div>
        </section>
      )}

      {coloBuckets.length > 0 && (
        <section className="card p-4">
          <h2 className="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            Egress geography
          </h2>
          <ColoMap buckets={coloBuckets} />
        </section>
      )}

      {coloBuckets.length > 0 && (
        <section className="card p-4">
          <h2 className="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            Workers per Cloudflare colo
          </h2>
          <div className="space-y-1.5">
            {coloBuckets.map(([colo, n]) => (
              <div key={colo} className="flex items-center gap-2 text-xs">
                <span className="w-12 shrink-0 font-mono">{colo}</span>
                <div className="h-3 flex-1 overflow-hidden rounded bg-muted">
                  <div className="h-full bg-accent/80" style={{ width: `${(n / coloMax) * 100}%` }} />
                </div>
                <span className="w-6 text-right font-mono tabular-nums">{n}</span>
              </div>
            ))}
          </div>
          <p className="mt-2 text-[11px] text-muted-foreground">
            Colo = Cloudflare point-of-presence reported by each Worker. More distinct colos = broader egress-IP geography.
          </p>
        </section>
      )}

      {topWorkers.length > 0 && topWorkers[0].requests > 0 && (
        <section className="card p-4">
          <h2 className="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            Top workers by request count (session)
          </h2>
          <div className="space-y-1.5">
            {topWorkers.map((w) => (
              <div key={w.name} className="flex items-center gap-2 text-xs">
                <span className="w-40 shrink-0 truncate font-mono" title={w.name}>{w.name}</span>
                <div className="h-3 flex-1 overflow-hidden rounded bg-muted">
                  <div className="h-full bg-accent" style={{ width: `${(w.requests / reqMax) * 100}%` }} />
                </div>
                <span className="w-16 text-right font-mono tabular-nums">{w.requests}</span>
              </div>
            ))}
          </div>
        </section>
      )}


      <section className="card p-4">
        <h2 className="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Request rate (req/s, 1-min buckets)</h2>
        {rateData.length === 0 ? (
          <div className="py-10 text-center text-sm text-muted-foreground">
            collecting samples — first chart data after 2 minutes of uptime
          </div>
        ) : (
          <ResponsiveContainer width="100%" height={200}>
            <LineChart data={rateData}>
              <XAxis dataKey="at" stroke="hsl(var(--muted-foreground))" fontSize={11} />
              <YAxis stroke="hsl(var(--muted-foreground))" fontSize={11} />
              <Tooltip
                contentStyle={{ background: "hsl(var(--card))", border: "1px solid hsl(var(--border))", borderRadius: 6, fontSize: 12 }}
                labelStyle={{ color: "hsl(var(--foreground))" }}
              />
              <Line type="monotone" dataKey="rps" stroke="#f38020" strokeWidth={2} dot={false} name="req/s" />
            </LineChart>
          </ResponsiveContainer>
        )}
      </section>

      <section className="card p-4">
        <h2 className="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Dial latency (ms, p50 / p95)</h2>
        {rateData.length === 0 ? (
          <div className="py-10 text-center text-sm text-muted-foreground">
            no data yet
          </div>
        ) : (
          <ResponsiveContainer width="100%" height={200}>
            <LineChart data={rateData}>
              <XAxis dataKey="at" stroke="hsl(var(--muted-foreground))" fontSize={11} />
              <YAxis stroke="hsl(var(--muted-foreground))" fontSize={11} />
              <Tooltip
                contentStyle={{ background: "hsl(var(--card))", border: "1px solid hsl(var(--border))", borderRadius: 6, fontSize: 12 }}
                labelStyle={{ color: "hsl(var(--foreground))" }}
              />
              <Line type="monotone" dataKey="p50" stroke="#6cc66c" strokeWidth={2} dot={false} name="p50" />
              <Line type="monotone" dataKey="p95" stroke="#f38020" strokeWidth={2} dot={false} name="p95" />
            </LineChart>
          </ResponsiveContainer>
        )}
      </section>
    </div>
  );
}
