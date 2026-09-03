/**
 * Every timestamp is Unix milliseconds UTC; formatting happens here.
 *
 * The clock and date order come from the viewer's own preferences rather than
 * being hard-coded, because "8/26/2026, 11:43:27 AM" is unreadable to most of
 * the world and 24-hour dd/mm is unreadable to the rest of it.
 */
import { prefs, dateLocale, dateParts, type Clock, type DateStyle } from "./prefs.svelte";
import { localeFor } from "./locale";

function hourOptions(clock: Clock): Intl.DateTimeFormatOptions {
  switch (clock) {
    case "h24":
      return { hour12: false };
    case "h12":
      return { hour12: true };
    default:
      return {};
  }
}

/** The absolute date and time, in the viewer's chosen shape. */
export function datetime(
  ts: number,
  opts: { seconds?: boolean; zone?: boolean } = {},
): string {
  const style = prefs.dateStyle;
  const time: Intl.DateTimeFormatOptions = {
    hour: "2-digit",
    minute: "2-digit",
    ...(opts.seconds ?? prefs.seconds ? { second: "2-digit" } : {}),
    ...(opts.zone ? { timeZoneName: "short" } : {}),
    ...hourOptions(prefs.clock),
  };
  return new Date(ts).toLocaleString(localeFor(dateLocale(style)), { ...dateParts(style), ...time });
}

/** Just the clock, for a dense column where the date is already established. */
export function clockTime(ts: number, seconds = prefs.seconds): string {
  return new Date(ts).toLocaleTimeString(localeFor(dateLocale(prefs.dateStyle)), {
    hour: "2-digit",
    minute: "2-digit",
    ...(seconds ? { second: "2-digit" } : {}),
    ...hourOptions(prefs.clock),
  });
}

/** Just the date. */
export function dateOnly(ts: number): string {
  const style = prefs.dateStyle;
  return new Date(ts).toLocaleDateString(localeFor(dateLocale(style)), dateParts(style));
}

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
  return dateOnly(ts);
}

/** The absolute value, for the title attribute on every relative timestamp. */
export function absolute(ts: number): string {
  return datetime(ts, { seconds: true, zone: true });
}

/** A sample of a date style, for the settings screen to show what it picked. */
export function sampleDate(style: DateStyle, clock: Clock, ts = Date.now()): string {
  const time: Intl.DateTimeFormatOptions = {
    hour: "2-digit",
    minute: "2-digit",
    ...(clock === "h24" ? { hour12: false } : clock === "h12" ? { hour12: true } : {}),
  };
  return new Date(ts).toLocaleString(localeFor(dateLocale(style)), { ...dateParts(style), ...time });
}

export function duration(ms: number): string {
  const seconds = Math.round(ms / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return minutes % 60 ? `${hours}h ${minutes % 60}m` : `${hours}h`;
  const days = Math.floor(hours / 24);
  return hours % 24 ? `${days}d ${hours % 24}h` : `${days}d`;
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
