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
  host_name: string;
  docker_host: string;
  snapshot_interval_ms: number;
  max_compose_file_bytes: number;
  ingest_rate_per_minute: number;
  metrics_public: boolean;
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

  // Authentication. Editable since 1.2.0; the client secret is not here
  // because it is write-only, like the notification targets and the ingest
  // token, and lives in the store's secrets rather than the draft.
  local_account: boolean;
  trust_proxy_auth: boolean;
  auth_header: string;
  auth_groups_header: string;
  admin_groups: string;
  trusted_proxies: string;
  oidc_issuer: string;
  oidc_client_id: string;
  oidc_redirect_url: string;
  oidc_scopes: string;
  oidc_username_claim: string;
  oidc_groups_claim: string;
  oidc_admin_groups: string;
  oidc_allowed_groups: string;
  oidc_allowed_users: string;
  session_ttl_ms: number;
  session_idle_ttl_ms: number;
  oidc_admin_ttl_ms: number;
  cookie_secure: string;
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
    host_name: "local",
    docker_host: "tcp://docker-socket-proxy:2375",
    snapshot_interval_ms: 300_000,
    max_compose_file_bytes: 1_048_576,
    ingest_rate_per_minute: 60,
    metrics_public: false,
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

    local_account: true,
    trust_proxy_auth: false,
    auth_header: "X-Remote-User",
    auth_groups_header: "X-Remote-Groups",
    admin_groups: "",
    trusted_proxies: "",
    oidc_issuer: "",
    oidc_client_id: "",
    oidc_redirect_url: "",
    oidc_scopes: "openid, profile, email",
    oidc_username_claim: "preferred_username",
    oidc_groups_claim: "groups",
    oidc_admin_groups: "",
    oidc_allowed_groups: "",
    oidc_allowed_users: "",
    session_ttl_ms: 2_592_000_000,
    session_idle_ttl_ms: 604_800_000,
    oidc_admin_ttl_ms: 43_200_000,
    cookie_secure: "auto",
  };
}

export function toDraft(e: Effective): Draft {
  return {
    host_name: e.host_name,
    docker_host: e.docker_host,
    snapshot_interval_ms: e.snapshot_interval_ms,
    max_compose_file_bytes: e.max_compose_file_bytes,
    ingest_rate_per_minute: e.ingest_rate_per_minute,
    metrics_public: e.metrics_public,
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

    local_account: e.local_account,
    trust_proxy_auth: e.trust_proxy_auth,
    auth_header: e.auth_header,
    auth_groups_header: e.auth_groups_header,
    admin_groups: e.admin_groups.join(", "),
    trusted_proxies: e.trusted_proxies.join(", "),
    oidc_issuer: e.oidc_issuer,
    oidc_client_id: e.oidc_client_id,
    oidc_redirect_url: e.oidc_redirect_url,
    oidc_scopes: e.oidc_scopes.join(", "),
    oidc_username_claim: e.oidc_username_claim,
    oidc_groups_claim: e.oidc_groups_claim,
    oidc_admin_groups: e.oidc_admin_groups.join(", "),
    oidc_allowed_groups: e.oidc_allowed_groups.join(", "),
    oidc_allowed_users: e.oidc_allowed_users.join(", "),
    session_ttl_ms: e.session_ttl_ms,
    session_idle_ttl_ms: e.session_idle_ttl_ms,
    oidc_admin_ttl_ms: e.oidc_admin_ttl_ms,
    cookie_secure: e.cookie_secure,
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
export type Secrets = { notifyUrls: string; ingestToken: string; oidcClientSecret: string };

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
    "session_ttl_ms",
    "session_idle_ttl_ms",
    "oidc_admin_ttl_ms",
    "max_compose_file_bytes",
    "ingest_rate_per_minute",
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

  // Plain strings and flags: compared one by one rather than in a loop,
  // because TypeScript can then check that each patch key takes the type the
  // draft field holds — a loop over names loses that and a list of settings
  // is exactly where a silent mistype costs something.
  const strings = [
    "auth_header",
    "auth_groups_header",
    "oidc_issuer",
    "oidc_client_id",
    "oidc_redirect_url",
    "oidc_username_claim",
    "oidc_groups_claim",
  ] as const;
  for (const key of strings) {
    if (draft[key] !== e[key]) patch[key] = draft[key];
  }
  const flags = ["local_account", "trust_proxy_auth"] as const;
  for (const key of flags) {
    if (draft[key] !== e[key]) patch[key] = draft[key];
  }
  if (draft.cookie_secure !== e.cookie_secure) {
    patch.cookie_secure = draft.cookie_secure as SettingsPatch["cookie_secure"];
  }

  const lists = [
    "admin_groups",
    "trusted_proxies",
    "oidc_scopes",
    "oidc_admin_groups",
    "oidc_allowed_groups",
    "oidc_allowed_users",
  ] as const;
  for (const key of lists) {
    const next = list(draft[key]);
    if (next.join(",") !== e[key].join(",")) patch[key] = next;
  }

  if (draft.host_name !== e.host_name) patch.host_name = draft.host_name;
  if (draft.docker_host !== e.docker_host) patch.docker_host = draft.docker_host;
  if (draft.metrics_public !== e.metrics_public) patch.metrics_public = draft.metrics_public;
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
  if (secrets.oidcClientSecret.trim() !== "") {
    patch.oidc_client_secret = secrets.oidcClientSecret.trim();
  }
  return patch;
}
