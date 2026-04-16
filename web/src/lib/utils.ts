export function cn(...classes: (string | undefined | false | null)[]): string {
  return classes.filter(Boolean).join(" ");
}

export function fmtAge(sec: number): string {
  if (sec < 60) return `${sec}s`;
  if (sec < 3600) return `${Math.floor(sec / 60)}m`;
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  return m ? `${h}h${String(m).padStart(2, "0")}m` : `${h}h`;
}

export function truncAcct(a: string): string {
  if (!a) return "-";
  return a.length > 12 ? `${a.slice(0, 8)}…` : a;
}

// acctLabel returns a user-friendly label for an Account: the CF display
// name with the noisy "'s Account" suffix stripped (CF auto-appends that
// to every email-derived account name). Falls back to truncated id.
export function acctLabel(a: { id: string; name?: string }): string {
  if (!a.name) return truncAcct(a.id);
  return a.name.replace(/['’]s Account$/i, "").trim() || truncAcct(a.id);
}

export function formatNumber(n: number): string {
  if (n < 1000) return String(n);
  if (n < 10_000) return `${(n / 1000).toFixed(1)}k`;
  if (n < 1_000_000) return `${Math.round(n / 1000)}k`;
  return `${(n / 1_000_000).toFixed(1)}M`;
}

export function fmtBytes(n: number): string {
  if (n < 1024) return `${n.toFixed(0)} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`;
  return `${(n / 1024 / 1024 / 1024).toFixed(2)} GB`;
}
