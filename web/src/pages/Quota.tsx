import { useEffect, useState } from "react";
import { Bar, BarChart, CartesianGrid, Cell, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { get, type QuotaDay } from "../lib/api";
import ErrorBanner from "../components/ErrorBanner";
import { truncAcct } from "../lib/utils";
import { usePageTitle } from "../lib/usePageTitle";

export default function Quota() {
  usePageTitle("Quota");
  const [rows, setRows] = useState<QuotaDay[]>([]);
  const [err, setErr] = useState<unknown>(null);

  useEffect(() => {
    async function load() {
      try {
        const r = await get<{ days: number; account: string; series: QuotaDay[] }>("/metrics/history?days=14");
        setRows(r.series || []);
        setErr(null);
      } catch (e) { setErr(e); }
    }
    load();
    const h = setInterval(load, 30_000);
    return () => clearInterval(h);
  }, []);

  // Totals per date across accounts.
  const byDate: Record<string, { date: string; used: number; limit: number }> = {};
  for (const q of rows) {
    if (!byDate[q.date]) byDate[q.date] = { date: q.date, used: 0, limit: 0 };
    byDate[q.date].used += q.used;
    byDate[q.date].limit += q.limit;
  }
  const chart = Object.values(byDate)
    .sort((a, b) => a.date.localeCompare(b.date))
    .map((d) => ({ date: d.date.slice(5), used: d.used, limit: d.limit, pct: d.limit > 0 ? (d.used / d.limit) * 100 : 0 }));

  return (
    <div className="space-y-6">
      <ErrorBanner error={err} />
      <section className="card p-4">
        <h2 className="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          Daily usage — last 14 days (all accounts)
        </h2>
        {chart.length === 0 ? (
          <div className="py-8 text-center text-sm text-muted-foreground">no history yet</div>
        ) : (
          <ResponsiveContainer width="100%" height={220}>
            <BarChart data={chart}>
              <CartesianGrid vertical={false} stroke="hsl(var(--border))" strokeDasharray="3 3" />
              <XAxis dataKey="date" stroke="hsl(var(--muted-foreground))" fontSize={11} />
              <YAxis stroke="hsl(var(--muted-foreground))" fontSize={11} />
              <Tooltip
                contentStyle={{ background: "hsl(var(--card))", border: "1px solid hsl(var(--border))", borderRadius: 6, fontSize: 12 }}
                labelStyle={{ color: "hsl(var(--foreground))" }}
                formatter={(v, n) => [v, n === "used" ? "used" : "limit"]}
              />
              <Bar dataKey="used" fill="#f38020" radius={[4, 4, 0, 0]}>
                {chart.map((d, i) => (
                  <Cell key={i} fill={d.pct >= 95 ? "#e05353" : d.pct >= 80 ? "#e6b84f" : "#f38020"} />
                ))}
              </Bar>
            </BarChart>
          </ResponsiveContainer>
        )}
      </section>

      <section className="card p-4">
        <h2 className="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          Per-account snapshots
        </h2>
        <table className="w-full text-sm">
          <thead className="bg-muted/50 text-xs uppercase tracking-wider text-muted-foreground">
            <tr>
              <th className="px-3 py-2 text-left">Date</th>
              <th className="px-3 py-2 text-left">Account</th>
              <th className="px-3 py-2 text-right">Used</th>
              <th className="px-3 py-2 text-right">Limit</th>
              <th className="px-3 py-2 text-right">%</th>
            </tr>
          </thead>
          <tbody className="font-mono text-xs">
            {rows.length === 0 && (
              <tr><td colSpan={5} className="px-3 py-6 text-center text-muted-foreground">no rows</td></tr>
            )}
            {rows
              .slice()
              .sort((a, b) => (b.date + b.account_id).localeCompare(a.date + a.account_id))
              .map((q, i) => {
                const pct = q.limit > 0 ? (q.used / q.limit) * 100 : 0;
                const pillCls = pct >= 95 ? "pill-err" : pct >= 80 ? "pill-warn" : "pill-ok";
                return (
                  <tr key={i} className="border-t">
                    <td className="px-3 py-2">{q.date}</td>
                    <td className="px-3 py-2 text-muted-foreground">{truncAcct(q.account_id)}</td>
                    <td className="px-3 py-2 text-right">{q.used}</td>
                    <td className="px-3 py-2 text-right">{q.limit}</td>
                    <td className="px-3 py-2 text-right">
                      <span className={`pill ${pillCls}`}>{pct.toFixed(1)}%</span>
                    </td>
                  </tr>
                );
              })}
          </tbody>
        </table>
      </section>
    </div>
  );
}
