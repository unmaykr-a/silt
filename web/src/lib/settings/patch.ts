/**
 * The settings form's data layer, with no Svelte in it.
 *
 * Extracted so it can be tested. The vitest config deliberately leaves the
 * Svelte plugin out, so a `.svelte.ts` rune module cannot be imported by a
 * test — and `buildPatch` is the one piece of this screen where being wrong is
 * silent. It decides what a save actually sends, and a bug that made it send
 * too much would write overrides for fields nobody touched, quietly detaching
 * them from the environment they were tracking.
 */

import type { Settings, SettingsPatch } from "$lib/api/client";

/** Everything in force, as the API reports it. */
export type Effective = Settings["effective"];

/**
 * The form's working copy.
 *
 * Kept separate from the loaded settings so a field being typed into is not
 * overwritten by a background refresh. List-shaped values are strings here
 * because that is what a text input holds; they are split on the way out.
 */
export type Draft = {
  snapshot_interval_ms: number;
  retention_days: number;
  unchanged_retention_days: number;
  event_retention_days: number;
  audit_retention_days: number;
  retention_interval_ms: number;
  vacuum_interval_ms: number;
  keep_keys: string;
  base_url: string;
  log_level: string;
  notify_on: string;
  notify_min_severity: string;
};

/**
 * A draft that is valid before anything has loaded.
 *
 * Non-null on purpose: every control lives in a snippet, and a snippet is a
 * hoisted function that no `{#if}` can narrow into. Rendering is gated on the
 * loaded settings instead.
 */
export function emptyDraft(): Draft {
  return {
    snapshot_interval_ms: 300_000,
    retention_days: 365,
    unchanged_retention_days: 7,
    event_retention_days: 90,
    audit_retention_days: 730,
    retention_interval_ms: 3_600_000,
    vacuum_interval_ms: 0,
    keep_keys: "",
    base_url: "",
    log_level: "info",
    notify_on: "",
    notify_min_severity: "medium",
  };
}

export function toDraft(e: Effective): Draft {
  return {
    snapshot_interval_ms: e.snapshot_interval_ms,
    retention_days: e.retention_days,
    unchanged_retention_days: e.unchanged_retention_days,
    event_retention_days: e.event_retention_days,
    audit_retention_days: e.audit_retention_days,
    retention_interval_ms: e.retention_interval_ms,
    vacuum_interval_ms: e.vacuum_interval_ms,
    keep_keys: e.keep_keys.join(", "),
    base_url: e.base_url,
    log_level: e.log_level,
    notify_on: e.notify_on.join(", "),
    notify_min_severity: e.notify_min_severity,
  };
}

/** Split a comma-separated field, dropping the empties a trailing comma leaves. */
export function list(value: string): string[] {
  return value
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
}

/** The same, for a textarea where newlines separate as well as commas. */
export function multiline(value: string): string[] {
  return value
    .split(/[\n,]/)
    .map((s) => s.trim())
    .filter(Boolean);
}

/** The write-only fields, which are never read back and so cannot be compared. */
export type Secrets = { notifyUrls: string; ingestToken: string };

/**
 * Only what actually differs from what is in force.
 *
 * The environment is the baseline and an override is a deliberate departure
 * from it, so a patch that restated every field would turn one save into
 * thirteen overrides — and every one of them would then stop tracking the
 * environment it was set from, silently, on the next container recreate.
 *
 * The two secrets are the exception: they are never returned by the API, so
 * there is nothing to compare them against, and they travel only when someone
 * has typed into them.
 */
export function buildPatch(draft: Draft, e: Effective, secrets: Secrets): SettingsPatch {
  const patch: SettingsPatch = {};

  const numbers = [
    "snapshot_interval_ms",
    "retention_days",
    "unchanged_retention_days",
    "event_retention_days",
    "audit_retention_days",
    "retention_interval_ms",
    "vacuum_interval_ms",
  ] as const;
  for (const key of numbers) {
    // Number() because an <input type="number"> hands back a string on some
    // paths, and a "365" that never equals 365 would make every save dirty.
    if (Number(draft[key]) !== e[key]) patch[key] = Number(draft[key]);
  }

  if (draft.base_url !== e.base_url) patch.base_url = draft.base_url;
  if (draft.log_level !== e.log_level) patch.log_level = draft.log_level as SettingsPatch["log_level"];
  if (draft.notify_min_severity !== e.notify_min_severity) {
    patch.notify_min_severity = draft.notify_min_severity as SettingsPatch["notify_min_severity"];
  }

  const keep = list(draft.keep_keys);
  if (keep.join(",") !== e.keep_keys.join(",")) patch.keep_keys = keep;
  const on = list(draft.notify_on);
  if (on.join(",") !== e.notify_on.join(",")) patch.notify_on = on;

  if (secrets.notifyUrls.trim() !== "") patch.notify_urls = multiline(secrets.notifyUrls);
  if (secrets.ingestToken.trim() !== "") patch.ingest_token = secrets.ingestToken.trim();
  return patch;
}
