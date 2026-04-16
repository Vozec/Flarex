import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Search, X, ArrowRight } from "lucide-react";
import { toast } from "sonner";
import { get, post } from "../lib/api";
import type { Account, StatusResp, Worker } from "../lib/api";
import { useConfirmDialog } from "./Dialog";

type Item = {
  id: string;
  label: string;
  hint?: string;
  group: string;
  run: () => void | Promise<void>;
};

// CommandPalette is a hand-rolled fuzzy search dialog that indexes the
// nav routes + live actions (kill all, recycle unhealthy) + workers +
// accounts. Opens on Cmd/Ctrl+K. Arrow keys + Enter.
export default function CommandPalette() {
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState("");
  const [workers, setWorkers] = useState<Worker[]>([]);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [cursor, setCursor] = useState(0);
  const nav = useNavigate();
  const confirm = useConfirmDialog();
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setOpen((o) => !o);
      } else if (e.key === "Escape" && open) {
        setOpen(false);
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open]);

  useEffect(() => {
    if (!open) return;
    setQ("");
    setCursor(0);
    setTimeout(() => inputRef.current?.focus(), 0);
    Promise.all([
      get<StatusResp>("/status").catch(() => ({ workers: [] as Worker[], pool_size: 0 })),
      get<{ accounts: Account[] }>("/accounts").catch(() => ({ accounts: [] })),
    ]).then(([s, a]) => { setWorkers(s.workers || []); setAccounts(a.accounts || []); });
  }, [open]);

  const items = useMemo<Item[]>(() => {
    const out: Item[] = [];
    const nav_ = (to: string, label: string, hint?: string) => out.push({
      id: "nav:" + to, label, hint, group: "Navigate",
      run: () => { nav(to); setOpen(false); },
    });
    nav_("/overview", "Overview", "dashboard");
    nav_("/accounts", "Accounts", "list + pause/resume");
    nav_("/workers", "Workers", "pool + recycle");
    nav_("/test", "Test request", "run a URL through the proxy");
    nav_("/logs", "Logs", "tail Cloudflare Worker logs");
    nav_("/quota", "Quota", "daily subrequest usage");
    nav_("/apikeys", "API keys", "scope-bound key registry");
    nav_("/audit", "Audit log", "recent admin mutations");
    nav_("/config", "Config", "sanitized runtime dump");

    out.push({
      id: "action:kill-all",
      label: "Pause ALL accounts (kill switch)",
      hint: "emergency stop",
      group: "Actions",
      run: async () => {
        setOpen(false);
        const ok = await confirm({
          title: "Pause all accounts?",
          description: "Marks every worker as quota-paused. Dials fail until resumed.",
          confirmLabel: "Pause all",
          variant: "danger",
        });
        if (!ok) return;
        const accs = await get<{ accounts: Account[] }>("/accounts");
        let n = 0;
        for (const a of accs.accounts || []) {
          const r = await post<{ affected: number }>(`/accounts/${encodeURIComponent(a.id)}/pause`);
          n += r.affected || 0;
        }
        toast.success(`Paused ${n} worker(s)`);
      },
    });
    out.push({
      id: "action:resume-all",
      label: "Resume ALL accounts",
      group: "Actions",
      run: async () => {
        setOpen(false);
        const accs = await get<{ accounts: Account[] }>("/accounts");
        let n = 0;
        for (const a of accs.accounts || []) {
          const r = await post<{ affected: number }>(`/accounts/${encodeURIComponent(a.id)}/resume`);
          n += r.affected || 0;
        }
        toast.success(`Resumed ${n} worker(s)`);
      },
    });
    out.push({
      id: "action:recycle-unhealthy",
      label: "Recycle unhealthy workers",
      group: "Actions",
      run: async () => {
        setOpen(false);
        const s = await get<StatusResp>("/status");
        const bad = (s.workers || []).filter((w) => !w.healthy && !w.quota_paused);
        if (bad.length === 0) { toast.info("No unhealthy workers."); return; }
        const ok = await confirm({
          title: `Recycle ${bad.length} unhealthy worker(s)?`,
          description: "Drains inflight + redeploys with a fresh egress IP.",
          confirmLabel: "Recycle",
        });
        if (!ok) return;
        let done = 0;
        for (const w of bad) {
          try { await post(`/workers/${encodeURIComponent(w.name)}/recycle`); done++; } catch { /* ignore */ }
        }
        toast.success(`Recycled ${done}/${bad.length}`);
      },
    });

    for (const w of workers) {
      out.push({
        id: "worker:" + w.name,
        label: w.name,
        hint: `${w.colo || "?"} · ${w.healthy ? "healthy" : "down"}`,
        group: "Workers",
        run: () => { nav(`/workers`); setOpen(false); },
      });
    }
    for (const a of accounts) {
      out.push({
        id: "account:" + a.id,
        label: a.id,
        hint: `${a.workers} workers, ${a.healthy} healthy`,
        group: "Accounts",
        run: () => { nav(`/accounts`); setOpen(false); },
      });
    }
    return out;
  }, [workers, accounts, nav, confirm]);

  const filtered = useMemo(() => {
    const s = q.trim().toLowerCase();
    if (!s) return items;
    return items.filter((it) =>
      it.label.toLowerCase().includes(s) ||
      (it.hint ?? "").toLowerCase().includes(s) ||
      it.group.toLowerCase().includes(s),
    );
  }, [items, q]);

  useEffect(() => { if (cursor >= filtered.length) setCursor(0); }, [filtered.length, cursor]);

  if (!open) return null;

  const groups = filtered.reduce<Record<string, Item[]>>((acc, it) => {
    (acc[it.group] ||= []).push(it); return acc;
  }, {});
  let flatIdx = 0;

  return (
    <div
      className="fixed inset-0 z-[60] flex items-start justify-center bg-black/50 p-4 pt-[10vh]"
      role="dialog" aria-modal="true"
      onMouseDown={(e) => { if (e.target === e.currentTarget) setOpen(false); }}
    >
      <div className="card w-full max-w-xl overflow-hidden">
        <div className="flex items-center gap-2 border-b px-3 py-2">
          <Search className="h-4 w-4 text-muted-foreground" />
          <input
            ref={inputRef}
            value={q}
            onChange={(e) => { setQ(e.target.value); setCursor(0); }}
            onKeyDown={(e) => {
              if (e.key === "ArrowDown") { e.preventDefault(); setCursor((c) => Math.min(filtered.length - 1, c + 1)); }
              else if (e.key === "ArrowUp") { e.preventDefault(); setCursor((c) => Math.max(0, c - 1)); }
              else if (e.key === "Enter") { e.preventDefault(); filtered[cursor]?.run(); }
            }}
            placeholder="Jump to anything… (accounts, workers, actions)"
            className="flex-1 bg-transparent text-sm outline-none"
          />
          <button onClick={() => setOpen(false)} className="rounded p-1 text-muted-foreground hover:bg-muted" aria-label="Close">
            <X className="h-4 w-4" />
          </button>
        </div>
        <div className="max-h-[60vh] overflow-auto">
          {filtered.length === 0 && (
            <div className="p-6 text-center text-xs text-muted-foreground">No matches.</div>
          )}
          {Object.entries(groups).map(([g, list]) => (
            <div key={g} className="py-1">
              <div className="px-3 py-1 text-[10px] uppercase tracking-wider text-muted-foreground">{g}</div>
              {list.map((it) => {
                const active = flatIdx === cursor;
                const myIdx = flatIdx++;
                return (
                  <button
                    key={it.id}
                    onMouseEnter={() => setCursor(myIdx)}
                    onClick={() => it.run()}
                    className={`flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm ${active ? "bg-accent/10 text-accent" : "hover:bg-muted"}`}
                  >
                    <span className="flex-1 truncate">{it.label}</span>
                    {it.hint && <span className="text-[11px] text-muted-foreground">{it.hint}</span>}
                    <ArrowRight className={`h-3 w-3 shrink-0 ${active ? "text-accent" : "opacity-30"}`} />
                  </button>
                );
              })}
            </div>
          ))}
        </div>
        <div className="border-t px-3 py-1.5 text-[10px] text-muted-foreground">
          ↑↓ navigate · ⏎ run · esc close · ⌘K to toggle
        </div>
      </div>
    </div>
  );
}
