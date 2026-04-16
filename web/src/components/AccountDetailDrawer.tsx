import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Copy, ExternalLink, Pause, Play, Plus, Trash2, X, Boxes } from "lucide-react";
import { get, post, del, type Account, type StatusResp, type Worker, type QuotaDay } from "../lib/api";
import { copy } from "../lib/clipboard";
import { accountDashboardURL } from "../lib/cfdash";
import { acctLabel, fmtAge, truncAcct } from "../lib/utils";
import { useConfirmDialog } from "./Dialog";

type Props = {
  account: Account | null;
  onClose: () => void;
  onChanged: () => void;
  onPickWorker?: (name: string) => void;
};

export default function AccountDetailDrawer({ account, onClose, onChanged, onPickWorker }: Props) {
  const confirm = useConfirmDialog();
  const [workers, setWorkers] = useState<Worker[]>([]);
  const [quota, setQuota] = useState<QuotaDay | null>(null);
  const [busy, setBusy] = useState(false);
  const [deployCount, setDeployCount] = useState<string>("");

  useEffect(() => {
    if (!account) return;
    function onKey(e: KeyboardEvent) { if (e.key === "Escape") onClose(); }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [account, onClose]);

  useEffect(() => {
    if (!account) return;
    let alive = true;
    async function tick() {
      try {
        const [s, q] = await Promise.all([
          get<StatusResp>("/status"),
          get<{ series: QuotaDay[] }>(`/metrics/history?days=1&account=${encodeURIComponent(account!.id)}`).catch(() => ({ series: [] })),
        ]);
        if (!alive) return;
        setWorkers((s.workers || []).filter((w) => w.account === account!.id));
        setQuota(q.series?.[0] || null);
      } catch {
        /* ignore transient errors */
      }
    }
    tick();
    const h = setInterval(tick, 10_000);
    return () => { alive = false; clearInterval(h); };
  }, [account]);

  if (!account) return null;
  const a = account;
  const paused = a.quota_paused === a.workers && a.workers > 0;

  async function pauseToggle() {
    setBusy(true);
    try {
      const r = await post<{ affected: number }>(`/accounts/${encodeURIComponent(a.id)}/${paused ? "resume" : "pause"}`);
      toast.success(`${paused ? "Resumed" : "Paused"} ${r.affected} worker(s)`);
      onChanged();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function deployMore() {
    const n = Number(deployCount);
    if (!Number.isFinite(n) || n <= 0) { toast.error("Count must be > 0"); return; }
    setBusy(true);
    try {
      const r = await post<{ deployed_workers?: string[] }>(`/accounts/${encodeURIComponent(a.id)}/deploy`, { count: n });
      toast.success(`Deployed ${r.deployed_workers?.length ?? 0} worker(s)`);
      setDeployCount("");
      onChanged();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function removeAll() {
    const ok = await confirm({
      title: `Remove ALL workers on ${truncAcct(a.id)}?`,
      description: "Deletes every Worker on this account from Cloudflare. The CF token stays valid; you can re-deploy later from the Accounts tab.",
      confirmLabel: "Remove all",
      variant: "danger",
    });
    if (!ok) return;
    setBusy(true);
    try {
      const r = await del<{ removed_workers?: string[] }>(`/tokens?account=${encodeURIComponent(a.id)}`);
      toast.success(`Removed ${r.removed_workers?.length ?? 0} worker(s)`);
      onChanged();
      onClose();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  const limit = quota?.limit || 100_000;
  const used = quota?.used || 0;
  const pct = Math.min(100, (used / limit) * 100);
  const tone = pct >= 90 ? "bg-red-500" : pct >= 75 ? "bg-amber-500" : "bg-accent";

  return (
    <>
      <div className="fixed inset-0 z-40 bg-black/50" onClick={onClose} aria-hidden />
      <aside
        className="fixed right-0 top-14 bottom-0 z-50 flex w-full max-w-md flex-col border-l bg-card shadow-xl md:w-[32rem]"
        role="dialog"
        aria-modal="true"
      >
        <header className="flex items-start justify-between gap-3 border-b p-4">
          <div className="min-w-0 flex-1">
            <div className="flex flex-col">
              <span className="text-sm font-semibold" title={a.id}>{acctLabel(a)}</span>
              {a.name && <span className="font-mono text-[11px] text-muted-foreground" title={a.id}>{truncAcct(a.id)}</span>}
            </div>
            <div className="mt-1 flex items-center gap-2">
              <button
                onClick={() => copy(a.id, "account id")}
                className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground"
                title="Copy account ID"
              >
                <Copy className="h-3 w-3" />
              </button>
              <a
                href={accountDashboardURL(a.id)}
                target="_blank" rel="noreferrer"
                className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground"
                title="Open on Cloudflare dashboard"
              >
                <ExternalLink className="h-3 w-3" />
              </a>
            </div>
            <div className="mt-0.5 text-xs text-muted-foreground">
              {a.workers} workers · {a.healthy} healthy · {a.quota_paused} quota-paused
            </div>
          </div>
          <button onClick={onClose} className="btn-ghost px-2 py-1" aria-label="Close">
            <X className="h-4 w-4" />
          </button>
        </header>

        <div className="flex-1 overflow-auto p-4 space-y-5">
          <section>
            <h3 className="mb-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
              Quota today
            </h3>
            <div className="text-xs">
              <div className="mb-1 flex items-center justify-between font-mono">
                <span>{used.toLocaleString()} / {limit.toLocaleString()} subrequests</span>
                <span className={pct >= 90 ? "text-red-500" : pct >= 75 ? "text-amber-500" : "text-muted-foreground"}>
                  {pct.toFixed(1)}%
                </span>
              </div>
              <div className="h-2 w-full overflow-hidden rounded bg-muted">
                <div className={`h-full ${tone} transition-all`} style={{ width: `${pct}%` }} />
              </div>
              {!quota && <div className="mt-1 text-[11px] text-muted-foreground">No quota snapshot yet — first write happens within 10 minutes of boot.</div>}
            </div>
          </section>

          <section>
            <h3 className="mb-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
              Workers ({workers.length})
            </h3>
            <div className="space-y-1">
              {workers.length === 0 && <div className="text-xs text-muted-foreground">no workers on this account</div>}
              {workers.map((w) => (
                <button
                  key={w.name}
                  onClick={() => onPickWorker?.(w.name)}
                  className="flex w-full items-center gap-2 rounded border px-2 py-1.5 text-left text-xs hover:bg-muted"
                >
                  <Boxes className="h-3 w-3 shrink-0 text-muted-foreground" />
                  <span className="flex-1 truncate font-mono">{w.name}</span>
                  <span className="text-muted-foreground">{w.colo || "—"}</span>
                  {w.quota_paused ? <span className="pill pill-warn">quota</span>
                    : w.healthy ? <span className="pill pill-ok">ok</span>
                    : <span className="pill pill-err">down</span>}
                  <span className="w-8 text-right text-muted-foreground">{fmtAge(w.age_sec)}</span>
                </button>
              ))}
            </div>
          </section>

          <section>
            <h3 className="mb-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
              Add more workers
            </h3>
            <div className="flex gap-2">
              <input
                type="number"
                min={1} max={100}
                value={deployCount}
                onChange={(e) => setDeployCount(e.target.value)}
                placeholder="count"
                className="input w-24 font-mono"
                disabled={busy}
              />
              <button onClick={deployMore} disabled={busy || !deployCount} className="btn-accent text-xs">
                <Plus className="h-3.5 w-3.5" /> {busy ? "Deploying…" : "Deploy"}
              </button>
            </div>
            <p className="mt-1 text-[11px] text-muted-foreground">
              Uses the CF API token already registered for this account — no re-auth needed.
            </p>
          </section>
        </div>

        <footer className="flex gap-2 border-t p-3">
          <button onClick={pauseToggle} disabled={busy} className="btn-ghost flex-1 text-xs">
            {paused ? <><Play className="h-3.5 w-3.5" /> Resume all</> : <><Pause className="h-3.5 w-3.5" /> Pause all</>}
          </button>
          <button onClick={removeAll} disabled={busy} className="btn-danger flex-1 text-xs">
            <Trash2 className="h-3.5 w-3.5" /> Remove all
          </button>
        </footer>
      </aside>
    </>
  );
}
