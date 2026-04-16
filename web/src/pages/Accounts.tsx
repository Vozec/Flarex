import { useEffect, useState } from "react";
import { Pause, Play, Plus, Trash2, Users } from "lucide-react";
import { toast } from "sonner";
import { del, get, post, type Account, type AccountsResp } from "../lib/api";
import ErrorBanner from "../components/ErrorBanner";
import EmptyState from "../components/EmptyState";
import { useConfirmDialog } from "../components/Dialog";
import AccountDetailDrawer from "../components/AccountDetailDrawer";
import { acctLabel, truncAcct } from "../lib/utils";
import { copy } from "../lib/clipboard";
import { accountDashboardURL } from "../lib/cfdash";
import { ExternalLink, Copy } from "lucide-react";
import { usePageTitle } from "../lib/usePageTitle";

export default function Accounts() {
  usePageTitle("Accounts");
  const confirm = useConfirmDialog();
  const [accts, setAccts] = useState<Account[]>([]);
  const [detailId, setDetailId] = useState<string | null>(null);
  const [err, setErr] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const [token, setToken] = useState("");
  const [count, setCount] = useState<number | "">("");

  async function load() {
    try {
      const r = await get<AccountsResp>("/accounts");
      setAccts(r.accounts || []);
      setErr(null);
    } catch (e) { setErr(e); }
  }
  useEffect(() => { load(); const h = setInterval(load, 10_000); return () => clearInterval(h); }, []);

  async function addToken(e: React.FormEvent) {
    e.preventDefault();
    if (!token) return;
    setBusy(true);
    try {
      const body: Record<string, unknown> = { token };
      if (typeof count === "number" && count > 0) body.count = count;
      const r = await post<{ deployed_workers?: string[] }>("/tokens", body);
      toast.success(`Deployed ${r.deployed_workers?.length ?? 0} worker(s)`);
      setToken(""); setCount("");
      await load();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function remove(id: string) {
    const acctRef = accts.find((x) => x.id === id);
    const label = acctRef ? acctLabel(acctRef) : truncAcct(id);
    const ok = await confirm({
      title: `Remove ALL workers on ${label}?`,
      description: <>This deletes every Worker on account <code className="font-mono">{id}</code> and cannot be undone. The CF token stays valid; you can re-deploy later.</>,
      confirmLabel: "Remove workers",
      variant: "danger",
    });
    if (!ok) return;
    try {
      const r = await del<{ removed_workers?: string[] }>(`/tokens?account=${encodeURIComponent(id)}`);
      toast.success(`Removed ${r.removed_workers?.length ?? 0} worker(s)`);
      await load();
    } catch (e: unknown) { toast.error(e instanceof Error ? e.message : String(e)); }
  }

  async function pauseToggle(id: string, paused: boolean) {
    try {
      const r = await post<{ affected: number }>(`/accounts/${encodeURIComponent(id)}/${paused ? "resume" : "pause"}`);
      toast.success(`${paused ? "Resumed" : "Paused"} ${r.affected} worker(s)`);
      await load();
    } catch (e: unknown) { toast.error(e instanceof Error ? e.message : String(e)); }
  }

  return (
    <div className="space-y-6">
      <ErrorBanner error={err} />

      <section className="card p-4">
        <h2 className="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Add a Cloudflare account</h2>
        <form onSubmit={addToken} className="flex flex-wrap gap-2">
          <input
            type="password"
            value={token}
            onChange={(e) => setToken(e.target.value)}
            placeholder="Cloudflare API token (cfut_…)"
            className="input flex-1 font-mono min-w-64"
            disabled={busy}
          />
          <input
            type="number"
            min={1} max={100}
            value={count}
            onChange={(e) => setCount(e.target.value ? Number(e.target.value) : "")}
            placeholder="count"
            className="input w-24 font-mono"
            disabled={busy}
          />
          <button type="submit" className="btn-accent" disabled={busy || !token}>
            <Plus className="h-4 w-4" /> {busy ? "Deploying…" : "Deploy"}
          </button>
        </form>
        <p className="mt-2 text-xs text-muted-foreground">
          Leaving <code>count</code> empty uses the server's <code>worker.count</code>. Token needs <code>Account.Workers Scripts: Edit</code> scope.
        </p>
      </section>

      <section className="card p-4">
        <h2 className="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Active accounts</h2>
        {accts.length === 0 ? (
          <EmptyState
            icon={Users}
            title="No accounts in the pool yet"
            desc="Paste a Cloudflare API token above to provision the first Workers."
          />
        ) : (
          <div className="grid gap-3 md:grid-cols-2 lg:grid-cols-3">
            {accts.map((a) => {
              const paused = a.quota_paused === a.workers && a.workers > 0;
              const healthRatio = a.workers === 0 ? 0 : a.healthy / a.workers;
              return (
                <div
                  key={a.id}
                  onClick={() => setDetailId(a.id)}
                  className="card cursor-pointer p-4 space-y-3 hover:border-accent/60 hover:shadow-md transition"
                >
                  <div className="flex items-start justify-between gap-2">
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-1">
                        <span className="text-sm font-semibold truncate" title={a.id}>{acctLabel(a)}</span>
                        {a.name && (
                          <span className="font-mono text-[11px] text-muted-foreground truncate" title={a.id}>
                            {truncAcct(a.id)}
                          </span>
                        )}
                        <button
                          onClick={(e) => { e.stopPropagation(); copy(a.id, "account id"); }}
                          className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground"
                          title="Copy account ID"
                          aria-label="Copy account ID"
                        >
                          <Copy className="h-3 w-3" />
                        </button>
                        <a
                          href={accountDashboardURL(a.id)}
                          target="_blank"
                          rel="noreferrer"
                          onClick={(e) => e.stopPropagation()}
                          className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground"
                          title="Open on Cloudflare dashboard"
                          aria-label="Open on Cloudflare dashboard"
                        >
                          <ExternalLink className="h-3 w-3" />
                        </a>
                      </div>
                      <div className="mt-1 text-xs text-muted-foreground">{a.workers} workers</div>
                    </div>
                    <span className={
                      `pill shrink-0 ${paused ? "pill-warn" : healthRatio >= 0.9 ? "pill-ok" : healthRatio >= 0.5 ? "pill-warn" : "pill-err"}`
                    }>
                      {paused ? "paused" : `${a.healthy}/${a.workers} healthy`}
                    </span>
                  </div>
                  <div className="flex gap-2">
                    <button
                      onClick={(e) => { e.stopPropagation(); pauseToggle(a.id, paused); }}
                      className="btn-ghost flex-1"
                    >
                      {paused ? <><Play className="h-3.5 w-3.5" /> Resume</> : <><Pause className="h-3.5 w-3.5" /> Pause</>}
                    </button>
                    <button
                      onClick={(e) => { e.stopPropagation(); remove(a.id); }}
                      className="btn-danger flex-1"
                    >
                      <Trash2 className="h-3.5 w-3.5" /> Remove
                    </button>
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </section>

      <AccountDetailDrawer
        account={detailId ? accts.find((a) => a.id === detailId) || null : null}
        onClose={() => setDetailId(null)}
        onChanged={load}
      />
    </div>
  );
}
