export function relativeTime(at?: string, now = Date.now()): string {
  if (!at || !Number.isFinite(Date.parse(at))) return '—';
  const seconds = Math.max(0, Math.floor((now - Date.parse(at)) / 1000));
  if (seconds < 60) return `hace ${seconds} s`;
  if (seconds < 3600) return `hace ${Math.floor(seconds / 60)} min`;
  if (seconds < 86400) return `hace ${Math.floor(seconds / 3600)} h`;
  return `hace ${Math.floor(seconds / 86400)} días`;
}
export const shortInstance = (id: string) => id.replace(/[^a-z0-9]/gi, '').slice(0, 8);
