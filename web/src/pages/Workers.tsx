import { useEffect, useMemo, useState } from "react";
import { ArrowDown, ArrowUp, ArrowUpDown, Boxes, RefreshCw, Zap } from "lucide-react";
import { toast } from "sonner";
import { get, post, type AccountsResp, type MetricsSample, type StatusResp, type Worker } from "../lib/api";
import ErrorBanner from "../components/ErrorBanner";
import EmptyState from "../components/EmptyState";
import Sparkline from "../components/Sparkline";
import WorkerDetailDrawer from "../components/WorkerDetailDrawer";
import { useConfirmDialog } from "../components/Dialog";
import { fmtAge, truncAcct } from "../lib/utils";
import { usePageTitle } from "../lib/usePageTitle";

type SortKey = "name" | "healthy" | "breaker" | "colo" | "account" | "inflight" | "requests" | "err_rate_ewma" | "age_sec";

export default function Workers() {
  usePageTitle("Workers");
  const confirm = useConfirmDialog();
  const [workers, setWorkers] = useState<Worker[]>([]);
  const [err, setErr] = useState<unknown>(null);
  const [filter, setFilter] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [detailName, setDetailName] = useState<string | null>(null);
  const [sortKey, setSortKey] = useState<SortKey>("name");
  const [sortDir, setSortDir] = useState<"asc" | "desc">("asc");

  const [samples, setSamples] = useState<MetricsSample[]>([]);
  const [accountNames, setAccountNames] = useState<Record<string, string>>({});

  async function load() {
    try {
      const [r, m, a] = await Promise.all([
        get<StatusResp>("/status"),
        get<{ samples: MetricsSample[] }>("/metrics/series").catch(() => ({ samples: [] })),
        get<AccountsResp>("/accounts").catch(() => ({ accounts: [] })),
      ]);
      setWorkers(r.workers || []);
      setSamples(m.samples || []);
      const names: Record<string, string> = {};
      for (const acc of a.accounts || []) {
        if (acc.name) names[acc.id] = acc.name;
      }
      setAccountNames(names);
      setErr(null);
    } catch (e) { setErr(e); }
  }
  useEffect(() => { load(); const h = setInterval(load, 10_000); return () => clearInterval(h); }, []);

  // Per-worker delta series (req/min) derived from consecutive samples.
  const perWorkerDelta = useMemo(() => {
    const out = new Map<string, number[]>();
    for (let i = 1; i < samples.length; i++) {
      const prev = samples[i - 1].worker_requests || {};
      const cur = samples[i].worker_requests || {};
      for (const name of new Set([...Object.keys(prev), ...Object.keys(cur)])) {
        const d = Math.max(0, (cur[name] || 0) - (prev[name] || 0));
        const arr = out.get(name) || [];
        arr.push(d);
        out.set(name, arr);
      }
    }
    return out;
  }, [samples]);

  const rows = useMemo(() => {
    const filtered = workers.filter((w) =>
      !filter ||
      w.name.toLowerCase().includes(filter.toLowerCase()) ||
      (w.colo ?? "").toLowerCase().includes(filter.toLowerCase()) ||
      w.account.includes(filter)
    );
    const mul = sortDir === "asc" ? 1 : -1;
    const sorted = [...filtered].sort((a, b) => {
      const av = (a as unknown as Record<string, unknown>)[sortKey];
      const bv = (b as unknown as Record<string, unknown>)[sortKey];
      if (typeof av === "number" && typeof bv === "number") return (av - bv) * mul;
      const as = String(av ?? "");
      const bs = String(bv ?? "");
      return as.localeCompare(bs) * mul;
    });
    return sorted;
  }, [workers, filter, sortKey, sortDir]);
  const unhealthy = useMemo(() => workers.filter((w) => !w.healthy && !w.quota_paused), [workers]);

  function toggleSort(k: SortKey) {
    if (sortKey === k) setSortDir(sortDir === "asc" ? "desc" : "asc");
    else { setSortKey(k); setSortDir("asc"); }
  }

  async function recycle(name: string) {
    setBusy(name);
    try {
      const r = await post<{ old: string; new: string }>(`/workers/${encodeURIComponent(name)}/recycle`);
      toast.success(`Recycled ${r.old} → ${r.new}`);
      await load();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  }

  async function recycleMany(names: string[], label: string) {
    if (names.length === 0) { toast.info("Nothing to recycle."); return; }
    const ok = await confirm({
      title: `Recycle ${names.length} ${label} worker(s)?`,
      description: "Each Worker drains inflight connections, is deleted, and a replacement is deployed with a fresh egress IP. Takes ~5–10 s per worker.",
      confirmLabel: `Recycle ${names.length}`,
    });
    if (!ok) return;
    setBusy("__bulk__");
    let done = 0, fail = 0;
    for (const n of names) {
      try {
        await post(`/workers/${encodeURIComponent(n)}/recycle`);
        done++;
      } catch {
        fail++;
      }
    }
    setBusy(null);
    setSelected(new Set());
    await load();
    if (fail === 0) toast.success(`Recycled ${done} ${label} worker(s)`);
    else toast.warning(`Recycled ${done} / ${done + fail} — ${fail} failed`);
  }

  function toggleOne(n: string) {
    const cp = new Set(selected);
    if (cp.has(n)) cp.delete(n); else cp.add(n);
    setSelected(cp);
  }
  function toggleAll() {
    if (selected.size === rows.length) setSelected(new Set());
    else setSelected(new Set(rows.map((w) => w.name)));
  }

  return (
    <div className="space-y-4">
      <ErrorBanner error={err} />
      <div className="flex flex-wrap items-center gap-3">
        <input
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder="filter by name / colo / account"
          className="input max-w-sm"
        />
        <span className="text-xs text-muted-foreground">{rows.length} / {workers.length}</span>
        <div className="flex-1" />
        {selected.size > 0 && (
          <button
            onClick={() => recycleMany(Array.from(selected), "selected")}
            className="btn-accent text-xs"
            disabled={busy !== null}
          >
            <RefreshCw className="h-3.5 w-3.5" /> Recycle selected ({selected.size})
          </button>
        )}
        {unhealthy.length > 0 && (
          <button
            onClick={() => recycleMany(unhealthy.map((w) => w.name), "unhealthy")}
            className="btn-ghost text-xs"
            disabled={busy !== null}
            title="Recycle all workers currently reporting healthy=false"
          >
            <Zap className="h-3.5 w-3.5" /> Recycle unhealthy ({unhealthy.length})
          </button>
        )}
      </div>

      {workers.length === 0 ? (
        <div className="card">
          <EmptyState
            icon={Boxes}
            title="No workers deployed"
            desc="Deploy workers from the Accounts tab, or run `flarex deploy` from the CLI."
          />
        </div>
      ) : (
        <div className="card overflow-auto">
          <table className="w-full text-sm">
            <thead className="bg-muted/50 text-xs uppercase tracking-wider text-muted-foreground">
              <tr>
                <th className="w-10 px-3 py-2">
                  <input
                    type="checkbox"
                    checked={selected.size === rows.length && rows.length > 0}
                    onChange={toggleAll}
                    aria-label="Select all"
                    className="accent-accent"
                  />
                </th>
                <SortHeader label="Name" k="name" sortKey={sortKey} sortDir={sortDir} onClick={toggleSort} />
                <SortHeader label="Health" k="healthy" sortKey={sortKey} sortDir={sortDir} onClick={toggleSort} />
                <SortHeader label="Breaker" k="breaker" sortKey={sortKey} sortDir={sortDir} onClick={toggleSort} />
                <SortHeader label="Colo" k="colo" sortKey={sortKey} sortDir={sortDir} onClick={toggleSort} />
                <SortHeader label="Account" k="account" sortKey={sortKey} sortDir={sortDir} onClick={toggleSort} />
                <SortHeader label="Inflight" k="inflight" sortKey={sortKey} sortDir={sortDir} onClick={toggleSort} align="right" />
                <SortHeader label="Req" k="requests" sortKey={sortKey} sortDir={sortDir} onClick={toggleSort} align="right" />
                <th className="px-3 py-2 text-left">Trend</th>
                <SortHeader label="Err%" k="err_rate_ewma" sortKey={sortKey} sortDir={sortDir} onClick={toggleSort} align="right" />
                <SortHeader label="Age" k="age_sec" sortKey={sortKey} sortDir={sortDir} onClick={toggleSort} align="right" />
                <th className="px-3 py-2 text-right"></th>
              </tr>
            </thead>
            <tbody className="font-mono text-xs">
              {rows.map((w) => {
                let health = <span className="pill pill-ok">ok</span>;
                if (w.quota_paused) health = <span className="pill pill-warn">quota</span>;
                else if (!w.healthy) health = <span className="pill pill-err">down</span>;
                let br = <span className="pill pill-neutral">{w.breaker}</span>;
                if (w.breaker === "open") br = <span className="pill pill-err">open</span>;
                else if (w.breaker === "half-open") br = <span className="pill pill-warn">half</span>;
                return (
                  <tr
                    key={w.name}
                    className="cursor-pointer border-t hover:bg-muted/30"
                    onClick={() => setDetailName(w.name)}
                  >
                    <td className="px-3 py-2" onClick={(e) => e.stopPropagation()}>
                      <input
                        type="checkbox"
                        checked={selected.has(w.name)}
                        onChange={() => toggleOne(w.name)}
                        aria-label={`Select ${w.name}`}
                        className="accent-accent"
                      />
                    </td>
                    <td className="px-3 py-2">{w.name}</td>
                    <td className="px-3 py-2">{health}</td>
                    <td className="px-3 py-2">{br}</td>
                    <td className="px-3 py-2">{w.colo || "-"}</td>
                    <td className="px-3 py-2 text-muted-foreground" title={w.account}>{accountNames[w.account] || truncAcct(w.account)}</td>
                    <td className="px-3 py-2 text-right">{w.inflight}</td>
                    <td className="px-3 py-2 text-right">{w.requests}</td>
                    <td className="px-3 py-2 text-accent">
                      <Sparkline values={perWorkerDelta.get(w.name) || []} />
                    </td>
                    <td className="px-3 py-2 text-right">{(w.err_rate_ewma * 100).toFixed(1)}%</td>
                    <td className="px-3 py-2 text-right">{fmtAge(w.age_sec)}</td>
                    <td className="px-3 py-2 text-right" onClick={(e) => e.stopPropagation()}>
                      <button
                        onClick={() => recycle(w.name)}
                        className="btn-ghost text-xs"
                        disabled={busy === w.name || busy === "__bulk__"}
                        title="Graceful drain + redeploy"
                      >
                        <RefreshCw className="h-3 w-3" />
                        {busy === w.name ? "recycling…" : "recycle"}
                      </button>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      <WorkerDetailDrawer
        worker={detailName ? workers.find((w) => w.name === detailName) || null : null}
        accountNames={accountNames}
        onClose={() => setDetailName(null)}
        onAction={load}
      />
    </div>
  );
}

function SortHeader({
  label, k, sortKey, sortDir, onClick, align,
}: {
  label: string;
  k: SortKey;
  sortKey: SortKey;
  sortDir: "asc" | "desc";
  onClick: (k: SortKey) => void;
  align?: "right";
}) {
  const active = sortKey === k;
  const Icon = !active ? ArrowUpDown : sortDir === "asc" ? ArrowUp : ArrowDown;
  return (
    <th className={`cursor-pointer select-none px-3 py-2 ${align === "right" ? "text-right" : "text-left"} hover:text-foreground`} onClick={() => onClick(k)}>
      <span className={`inline-flex items-center gap-1 ${align === "right" ? "flex-row-reverse" : ""}`}>
        {label}
        <Icon className={`h-3 w-3 ${active ? "text-accent" : "opacity-40"}`} />
      </span>
    </th>
  );
}
