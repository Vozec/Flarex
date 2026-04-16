export function workerDashboardURL(accountID: string, workerName: string): string {
  return `https://dash.cloudflare.com/${encodeURIComponent(accountID)}/workers/services/view/${encodeURIComponent(workerName)}/production`;
}

export function accountDashboardURL(accountID: string): string {
  return `https://dash.cloudflare.com/${encodeURIComponent(accountID)}/workers-and-pages`;
}
