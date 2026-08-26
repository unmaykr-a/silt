/** Every timestamp is Unix milliseconds UTC; formatting happens here. */

export function relative(ts: number, now: number = Date.now()): string {
  const seconds = Math.round((now - ts) / 1000);
  if (seconds < 5) return "just now";
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.round(hours / 24);
  if (days < 30) return `${days}d ago`;
  return new Date(ts).toLocaleDateString();
}

/** The absolute value, for the title attribute on every relative timestamp. */
export function absolute(ts: number): string {
  return new Date(ts).toLocaleString(undefined, { timeZoneName: "short" });
}

export function duration(ms: number): string {
  const seconds = Math.round(ms / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ${minutes % 60}m`;
  return `${Math.floor(hours / 24)}d ${hours % 24}h`;
}

export function bytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 ** 2) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 ** 3) return `${(n / 1024 ** 2).toFixed(1)} MB`;
  return `${(n / 1024 ** 3).toFixed(2)} GB`;
}

/** Digests are long and only their prefix is ever legible at a glance. */
export function shortDigest(digest: string | undefined, length = 12): string {
  if (!digest) return "";
  const bare = digest.startsWith("sha256:") ? digest.slice(7) : digest;
  return bare.slice(0, length);
}

export function severityClass(severity: string): string {
  switch (severity) {
    case "high":
    case "error":
      return "text-red-400";
    case "medium":
    case "warn":
      return "text-amber-400";
    default:
      return "text-muted-foreground";
  }
}

export function severityDot(severity: string): string {
  switch (severity) {
    case "high":
    case "error":
      return "bg-red-500";
    case "medium":
    case "warn":
      return "bg-amber-500";
    default:
      return "bg-zinc-500";
  }
}
