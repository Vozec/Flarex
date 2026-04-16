import { useEffect, useState } from "react";
import { Check, Copy, KeyRound, Power, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { del, get, patch, post, type APIKey, type APIKeysResp, type CreateAPIKeyResp } from "../lib/api";
import ErrorBanner from "../components/ErrorBanner";
import EmptyState from "../components/EmptyState";
import { useConfirmDialog } from "../components/Dialog";
import { usePageTitle } from "../lib/usePageTitle";

const ALL_SCOPES = [
  { id: "read", desc: "GETs (status, metrics, logs, config, audit)" },
  { id: "write", desc: "POST/DELETE tokens, workers, accounts (pause/resume)" },
  { id: "apikeys", desc: "manage other keys (privileged — escalation risk)" },
  { id: "pprof", desc: "/debug/pprof/* (leaks internals)" },
];

const TTL_OPTIONS = [
  { label: "Never (manual revoke)", value: "" },
  { label: "24 hours", value: "24h" },
  { label: "7 days", value: "168h" },
  { label: "30 days", value: "720h" },
  { label: "90 days", value: "2160h" },
];

export default function APIKeys() {
  usePageTitle("API keys");
  const confirm = useConfirmDialog();
  const [keys, setKeys] = useState<APIKey[]>([]);
  const [err, setErr] = useState<unknown>(null);
  const [newName, setNewName] = useState("");
  const [newScopes, setNewScopes] = useState<string[]>(["read"]);
  const [newTTL, setNewTTL] = useState("");
  const [created, setCreated] = useState<CreateAPIKeyResp | null>(null);
  const [copied, setCopied] = useState(false);

  async function load() {
    try {
      const r = await get<APIKeysResp>("/apikeys");
      setKeys(r.keys || []);
      setErr(null);
    } catch (e) { setErr(e); }
  }
  useEffect(() => { load(); }, []);

  async function create(e: React.FormEvent) {
    e.preventDefault();
    if (!newName) return;
    try {
      const body: Record<string, unknown> = { name: newName, scopes: newScopes };
      if (newTTL) body.expires_in = newTTL;
      const r = await post<CreateAPIKeyResp>("/apikeys", body);
      setCreated(r);
      setNewName(""); setNewScopes(["read"]); setNewTTL("");
      toast.success(`Key "${r.name}" created`);
      await load();
    } catch (e: unknown) { toast.error(e instanceof Error ? e.message : String(e)); }
  }
  async function toggleDisabled(k: APIKey) {
    try {
      await patch(`/apikeys/${encodeURIComponent(k.id)}`, { disabled: !k.disabled });
      toast.success(`${k.disabled ? "Enabled" : "Disabled"} "${k.name}"`);
      await load();
    } catch (e: unknown) { toast.error(e instanceof Error ? e.message : String(e)); }
  }
  async function remove(k: APIKey) {
    const ok = await confirm({
      title: `Revoke key "${k.name}"?`,
      description: <>Prefix <code className="font-mono">{k.prefix}…</code> — this is immediate and cannot be undone. Any caller using this key will get a 401 on the next request.</>,
      confirmLabel: "Revoke",
      variant: "danger",
    });
    if (!ok) return;
    try {
      await del(`/apikeys/${encodeURIComponent(k.id)}`);
      toast.success(`Revoked "${k.name}"`);
      await load();
    } catch (e: unknown) { toast.error(e instanceof Error ? e.message : String(e)); }
  }
  function copy(v: string) {
    navigator.clipboard.writeText(v);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  }

  return (
    <div className="space-y-6">
      <ErrorBanner error={err} />

      {created && (
        <section className="card border-accent/40 bg-accent/5 p-4">
          <div className="flex items-start gap-3">
            <KeyRound className="text-accent mt-0.5 h-5 w-5" />
            <div className="flex-1">
              <div className="text-sm font-semibold">Key "{created.name}" created</div>
              <p className="mt-1 text-xs text-muted-foreground">
                Copy the raw key now — it will never be shown again. Store it in your password manager.
              </p>
              <div className="mt-3 flex items-center gap-2">
                <code className="flex-1 overflow-x-auto rounded border bg-background px-3 py-2 font-mono text-sm">
                  {created.key}
                </code>
                <button onClick={() => copy(created.key)} className="btn-ghost" title="Copy">
                  {copied ? <Check className="h-4 w-4 text-emerald-500" /> : <Copy className="h-4 w-4" />}
                </button>
              </div>
              <div className="mt-2 flex gap-2 flex-wrap">
                {created.scopes.map((s) => <span key={s} className="pill pill-ok">{s}</span>)}
                {created.expires_at && (
                  <span className="pill pill-warn">expires {new Date(created.expires_at).toLocaleDateString()}</span>
                )}
              </div>
              <button onClick={() => setCreated(null)} className="btn-ghost mt-3 text-xs">Got it, dismiss</button>
            </div>
          </div>
        </section>
      )}

      <section className="card p-4">
        <h2 className="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Create a new key</h2>
        <form onSubmit={create} className="grid gap-4 md:grid-cols-12">
          <div className="md:col-span-5">
            <label className="mb-1 block text-xs font-medium text-muted-foreground">Name</label>
            <input
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              placeholder="scraper-prod"
              className="input w-full"
            />
          </div>

          <div className="md:col-span-4">
            <label className="mb-1 block text-xs font-medium text-muted-foreground">Expires</label>
            <select value={newTTL} onChange={(e) => setNewTTL(e.target.value)} className="input w-full">
              {TTL_OPTIONS.map((t) => <option key={t.value} value={t.value}>{t.label}</option>)}
            </select>
          </div>

          <div className="md:col-span-3 md:flex md:items-end">
            <button type="submit" className="btn-accent w-full" disabled={!newName || newScopes.length === 0}>
              <KeyRound className="h-4 w-4" /> Create key
            </button>
          </div>

          <div className="md:col-span-12">
            <label className="mb-1 block text-xs font-medium text-muted-foreground">Scopes</label>
            <div className="grid gap-2 sm:grid-cols-2">
              {ALL_SCOPES.map((s) => {
                const active = newScopes.includes(s.id);
                return (
                  <label
                    key={s.id}
                    className={`flex cursor-pointer items-start gap-2 rounded border p-2 text-sm transition ${active ? "border-accent/70 bg-accent/5" : "border-border hover:bg-muted"}`}
                  >
                    <input
                      type="checkbox"
                      checked={active}
                      onChange={(e) => {
                        if (e.target.checked) setNewScopes([...newScopes, s.id]);
                        else setNewScopes(newScopes.filter((x) => x !== s.id));
                      }}
                      className="accent-accent mt-0.5"
                    />
                    <div className="flex-1">
                      <div className="font-mono text-xs font-semibold">{s.id}</div>
                      <div className="text-[11px] text-muted-foreground">{s.desc}</div>
                    </div>
                  </label>
                );
              })}
            </div>
          </div>
        </form>
      </section>

      <section className="card p-4">
        <h2 className="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Existing keys</h2>
        {keys.length === 0 ? (
          <EmptyState
            icon={KeyRound}
            title="No named keys yet"
            desc="Create a scoped key above — safer than handing out the bootstrap `admin.api_key`."
          />
        ) : (
          <table className="w-full text-sm">
            <thead className="bg-muted/50 text-xs uppercase tracking-wider text-muted-foreground">
              <tr>
                <th className="px-3 py-2 text-left">Name</th>
                <th className="px-3 py-2 text-left">Prefix</th>
                <th className="px-3 py-2 text-left">Scopes</th>
                <th className="px-3 py-2 text-left">Status</th>
                <th className="px-3 py-2 text-left">Expires</th>
                <th className="px-3 py-2 text-left">Created</th>
                <th className="px-3 py-2 text-left">Last used</th>
                <th className="px-3 py-2 text-right"></th>
              </tr>
            </thead>
            <tbody className="font-mono text-xs">
              {keys.map((k) => {
                const expiring = k.expires_at ? new Date(k.expires_at).getTime() - Date.now() < 7 * 86400_000 : false;
                return (
                  <tr key={k.id} className="border-t">
                    <td className="px-3 py-2">{k.name}</td>
                    <td className="px-3 py-2 text-muted-foreground">{k.prefix}…</td>
                    <td className="px-3 py-2">
                      <div className="flex flex-wrap gap-1">
                        {k.scopes.map((s) => <span key={s} className="pill pill-neutral">{s}</span>)}
                      </div>
                    </td>
                    <td className="px-3 py-2">
                      {k.expired ? <span className="pill pill-err">expired</span>
                        : k.disabled ? <span className="pill pill-err">disabled</span>
                        : <span className="pill pill-ok">active</span>}
                    </td>
                    <td className="px-3 py-2 text-muted-foreground">
                      {k.expires_at ? (
                        <span className={expiring ? "text-amber-500" : ""}>
                          {new Date(k.expires_at).toLocaleDateString()}
                        </span>
                      ) : "never"}
                    </td>
                    <td className="px-3 py-2 text-muted-foreground">{new Date(k.created_at).toLocaleDateString()}</td>
                    <td className="px-3 py-2 text-muted-foreground">{k.last_used_at ? new Date(k.last_used_at).toLocaleString() : "—"}</td>
                    <td className="px-3 py-2 text-right">
                      <div className="flex justify-end gap-2">
                        <button onClick={() => toggleDisabled(k)} className="btn-ghost text-xs" title={k.disabled ? "Enable" : "Disable"}>
                          <Power className="h-3 w-3" /> {k.disabled ? "enable" : "disable"}
                        </button>
                        <button onClick={() => remove(k)} className="btn-danger text-xs" title="Revoke">
                          <Trash2 className="h-3 w-3" /> revoke
                        </button>
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </section>
    </div>
  );
}
