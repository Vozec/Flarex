import { useEffect, useState } from "react";
import { Activity, Download } from "lucide-react";
import { get, type AuditEvent, type AuditResp } from "../lib/api";
import ErrorBanner from "../components/ErrorBanner";
import EmptyState from "../components/EmptyState";
import { usePageTitle } from "../lib/usePageTitle";

export default function Audit() {
  usePageTitle("Audit log");
  const [rows, setRows] = useState<AuditEvent[]>([]);
  const [err, setErr] = useState<unknown>(null);
  const [limit, setLimit] = useState(200);

  async function load() {
    try {
      const r = await get<AuditResp>(`/audit?limit=${limit}`);
      setRows(r.events || []);
      setErr(null);
    } catch (e) {
      setErr(e);
    }
  }
  useEffect(() => { load(); const h = setInterval(load, 15000); return () => clearInterval(h); }, [limit]);

  function exportCSV() {
    const header = ["at", "who", "action", "target", "detail"];
    const escape = (s: string) => {
      const v = s ?? "";
      return /[",\n]/.test(v) ? `"${v.replace(/"/g, '""')}"` : v;
    };
    const lines = [header.join(",")];
    for (const e of rows) {
      lines.push([e.at, e.who, e.action, e.target, e.detail ?? ""].map(escape).join(","));
    }
    const blob = new Blob([lines.join("\n") + "\n"], { type: "text/csv;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const date = new Date().toISOString().slice(0, 10);
    const a = document.createElement("a");
    a.href = url;
    a.download = `flarex-audit-${date}.csv`;
    a.click();
    URL.revokeObjectURL(url);
  }

  return (
    <div className="space-y-4">
      <ErrorBanner error={err} />
      <div className="flex items-center gap-3">
        <label className="text-xs text-muted-foreground">limit</label>
        <select
          value={limit}
          onChange={(e) => setLimit(Number(e.target.value))}
          className="input w-24"
        >
          <option>50</option><option>200</option><option>500</option><option>1000</option>
        </select>
        <span className="text-xs text-muted-foreground">showing {rows.length} events</span>
        <div className="flex-1" />
        <button onClick={exportCSV} className="btn-ghost text-xs" disabled={rows.length === 0} title="Export visible rows as CSV">
          <Download className="h-3.5 w-3.5" /> CSV
        </button>
      </div>
      <div className="card overflow-auto">
        {rows.length === 0 ? (
          <EmptyState
            icon={Activity}
            title="No audit events yet"
            desc="Admin mutations (add/remove token, recycle worker, pause account, create/revoke API key) show up here."
          />
        ) : (
          <table className="w-full text-sm">
            <thead className="bg-muted/50 text-xs uppercase tracking-wider text-muted-foreground">
              <tr>
                <th className="px-3 py-2 text-left">When</th>
                <th className="px-3 py-2 text-left">Who</th>
                <th className="px-3 py-2 text-left">Action</th>
                <th className="px-3 py-2 text-left">Target</th>
                <th className="px-3 py-2 text-left">Detail</th>
              </tr>
            </thead>
            <tbody className="font-mono text-xs">
              {rows.map((e, i) => {
                const kind = e.action.endsWith(".failed") ? "pill-err"
                  : e.action.startsWith("apikey.revoke") || e.action.startsWith("token.remove") ? "pill-warn"
                  : "pill-ok";
                return (
                  <tr key={i} className="border-t">
                    <td className="px-3 py-2 text-muted-foreground whitespace-nowrap">
                      {new Date(e.at).toLocaleString()}
                    </td>
                    <td className="px-3 py-2">{e.who}</td>
                    <td className="px-3 py-2"><span className={`pill ${kind}`}>{e.action}</span></td>
                    <td className="px-3 py-2 text-muted-foreground">{e.target || "—"}</td>
                    <td className="px-3 py-2 text-muted-foreground">{e.detail || ""}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
