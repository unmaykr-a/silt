/**
 * What is on the settings screen, and how to find it.
 *
 * Nine sections and forty-odd fields is past the point where "it's in here
 * somewhere" works. Anyone who has gone looking for the keep-list has clicked
 * through Collection, Retention and Security to find it, and the number of
 * sections only goes up.
 *
 * So the index is data rather than markup: a searchable record of every
 * setting, the section it lives in, and the environment variable behind it.
 * Searching by variable name matters as much as by label — the compose file is
 * where people actually know these by, and `SILT_KEEP_KEYS` is the name in
 * hand when the question comes up.
 *
 * Kept beside the screen rather than generated from it because the screen is
 * markup, and a test that reads markup pins the markup. The one thing that can
 * drift is a field on screen with no entry here, which
 * settingsindex.test.ts checks against the rendered field names.
 */

/** Where a setting lives. Ordered as the rail orders them. */
export type SectionID =
  | "setup"
  | "appearance"
  | "collection"
  | "retention"
  | "notifications"
  | "ingest"
  | "security"
  | "identity"
  | "environment"
  | "storage";

export type SettingEntry = {
  /** The field name, matching the `name` the screen renders it under. */
  name: string;
  section: SectionID;
  label: string;
  /** The environment variable, where there is one. */
  env?: string;
  /** Words worth matching that are in neither the label nor the variable. */
  keywords?: string;
};

export const SECTIONS: { id: SectionID; label: string }[] = [
  { id: "setup", label: "Setup" },
  { id: "appearance", label: "Appearance" },
  { id: "collection", label: "Collection" },
  { id: "retention", label: "Retention" },
  { id: "notifications", label: "Notifications" },
  { id: "ingest", label: "Ingest webhook" },
  { id: "security", label: "Security" },
  { id: "identity", label: "Authentication" },
  { id: "environment", label: "Environment only" },
  { id: "storage", label: "Storage" },
];

export const SETTINGS: SettingEntry[] = [
  { name: "checks", section: "setup", label: "Configuration review", keywords: "warnings problems health is this set up correctly" },
  { name: "probes", section: "setup", label: "Live checks", keywords: "does it work docker reachable mounted test connection probe" },

  // Appearance — this browser, not the install.
  { name: "theme", section: "appearance", label: "Theme", keywords: "dark light system colour color" },
  { name: "layout", section: "appearance", label: "Navigation", keywords: "top bar left rail sidebar" },
  { name: "clock", section: "appearance", label: "Clock", keywords: "24 hour 12 am pm time format" },
  { name: "dateStyle", section: "appearance", label: "Date order", keywords: "dd mm yyyy iso" },
  { name: "timestamps", section: "appearance", label: "Timestamps", keywords: "relative absolute ago seconds" },

  // Collection.
  { name: "snapshot_interval_ms", section: "collection", label: "Reconcile interval", env: "SILT_SNAPSHOT_INTERVAL", keywords: "poll cadence how often" },
  { name: "keep_keys", section: "collection", label: "Keys kept readable", env: "SILT_KEEP_KEYS", keywords: "redaction safe list secrets cleartext" },
  { name: "log_level", section: "collection", label: "Log level", env: "SILT_LOG_LEVEL", keywords: "debug verbose" },

  // Retention.
  { name: "retention_days", section: "retention", label: "Changed snapshots", env: "SILT_RETENTION_DAYS", keywords: "how long keep history prune" },
  { name: "unchanged_retention_days", section: "retention", label: "Runtime-only snapshots", env: "SILT_UNCHANGED_RETENTION_DAYS", keywords: "proof of liveness restarts health" },
  { name: "event_retention_days", section: "retention", label: "Events", env: "SILT_EVENT_RETENTION_DAYS" },
  { name: "audit_retention_days", section: "retention", label: "Activity trail", env: "SILT_AUDIT_RETENTION_DAYS", keywords: "audit who changed" },
  { name: "retention_interval_ms", section: "retention", label: "Retention pass runs every", env: "SILT_RETENTION_INTERVAL" },
  { name: "vacuum_interval_ms", section: "retention", label: "Vacuum", env: "SILT_VACUUM_INTERVAL", keywords: "reclaim disk space sqlite" },

  // Notifications.
  { name: "notify_urls", section: "notifications", label: "Targets", env: "SILT_NOTIFY_URLS", keywords: "shoutrrr ntfy gotify discord telegram email webhook" },
  { name: "notify_on", section: "notifications", label: "Notify on", env: "SILT_NOTIFY_ON", keywords: "change kinds filter image volumes" },
  { name: "notify_min_severity", section: "notifications", label: "Minimum severity", env: "SILT_NOTIFY_MIN_SEVERITY", keywords: "high medium low threshold" },
  { name: "base_url", section: "notifications", label: "Base URL", env: "SILT_BASE_URL", keywords: "public link notification" },

  // Ingest.
  { name: "ingest_token", section: "ingest", label: "Token", env: "SILT_INGEST_TOKEN", keywords: "webhook uptime kuma external events api" },

  // Security and identity — read-only, and the reason the index exists: an
  // operator hunting for why forward auth is not working has no idea these
  // are on a screen at all.
  { name: "sessions", section: "security", label: "Sessions", env: "SILT_SESSION_TTL", keywords: "sign out revoke devices" },
  { name: "password", section: "security", label: "Password", env: "SILT_PASSWORD_HASH", keywords: "change bcrypt login account" },
  { name: "activity", section: "security", label: "Activity", keywords: "audit log who changed signed in refused" },
  { name: "auth_mode", section: "identity", label: "Authentication method", keywords: "oidc proxy password none how do i log in" },
  { name: "trust_proxy_auth", section: "identity", label: "Forward auth", env: "SILT_TRUST_PROXY_AUTH", keywords: "authelia authentik tinyauth reverse proxy header" },
  { name: "auth_header", section: "identity", label: "Identity header", env: "SILT_AUTH_HEADER", keywords: "x-remote-user forward auth" },
  { name: "trusted_proxies", section: "identity", label: "Trusted proxies", env: "SILT_TRUSTED_PROXIES", keywords: "cidr subnet forward auth" },
  { name: "oidc_issuer", section: "identity", label: "OpenID Connect", env: "SILT_OIDC_ISSUER", keywords: "oidc sso provider keycloak authentik" },
  { name: "oidc_client", section: "identity", label: "OIDC client", env: "SILT_OIDC_CLIENT_ID", keywords: "client id secret" },
  { name: "oidc_claims", section: "identity", label: "OIDC claims", env: "SILT_OIDC_USERNAME_CLAIM", keywords: "groups username claim" },
  { name: "oidc_allowed", section: "identity", label: "Who may sign in", env: "SILT_OIDC_ALLOWED_GROUPS", keywords: "allowlist groups users restrict" },
  { name: "roles", section: "identity", label: "Roles", keywords: "admin administrator viewer read only readonly permissions who can change" },
  { name: "oidc_admin_groups", section: "identity", label: "Administrator groups", env: "SILT_OIDC_ADMIN_GROUPS", keywords: "admin role viewer read only oidc" },
  { name: "admin_groups", section: "identity", label: "Administrator groups (forward auth)", env: "SILT_ADMIN_GROUPS", keywords: "admin role viewer read only proxy" },
  { name: "auth_groups_header", section: "identity", label: "Groups header", env: "SILT_AUTH_GROUPS_HEADER", keywords: "x-remote-groups forward auth admin" },
  { name: "local_account", section: "identity", label: "Built-in account", env: "SILT_LOCAL_ACCOUNT" },
  { name: "metrics_public", section: "identity", label: "Public metrics", env: "SILT_METRICS_PUBLIC", keywords: "prometheus scrape unauthenticated" },

  // Environment only.
  { name: "host_name", section: "environment", label: "Host name", env: "SILT_HOST_NAME" },
  { name: "docker_host", section: "environment", label: "Docker endpoint", env: "SILT_DOCKER_HOST", keywords: "socket proxy" },
  { name: "db_path", section: "environment", label: "Database", env: "SILT_DB_PATH", keywords: "sqlite file path" },
  { name: "listen_addr", section: "environment", label: "Listen address", env: "SILT_LISTEN_ADDR", keywords: "port bind" },
  { name: "compose_roots", section: "environment", label: "Compose roots", env: "SILT_COMPOSE_ROOTS", keywords: "files capture allowlist mount" },
  { name: "max_compose_file_bytes", section: "environment", label: "Max compose file", env: "SILT_MAX_COMPOSE_FILE_BYTES" },

  // Storage.
  { name: "usage", section: "storage", label: "Storage used", keywords: "size disk blobs deduplicated" },
  { name: "prune", section: "storage", label: "Prune now", keywords: "delete old garbage collect vacuum reclaim" },
  { name: "export", section: "storage", label: "Download settings", keywords: "export import backup restore move migrate json" },
];

export type SearchHit = SettingEntry & { sectionLabel: string };

/**
 * Settings matching `query`, best first.
 *
 * Substring rather than fuzzy: a settings search that answers "keep" with
 * "Vacuum" because the letters appear in order is worse than one that answers
 * nothing. Every term must match somewhere, so "notify severity" narrows
 * rather than widens.
 */
export function searchSettings(query: string, entries: SettingEntry[] = SETTINGS): SearchHit[] {
  const terms = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (terms.length === 0) return [];

  const sectionLabel = new Map(SECTIONS.map((s) => [s.id, s.label]));

  const scored: { hit: SearchHit; score: number }[] = [];
  for (const entry of entries) {
    const label = entry.label.toLowerCase();
    const env = (entry.env ?? "").toLowerCase();
    const section = (sectionLabel.get(entry.section) ?? "").toLowerCase();
    const haystack = [label, env, section, entry.keywords ?? "", entry.name].join(" ").toLowerCase();

    if (!terms.every((t) => haystack.includes(t))) continue;

    // A label match beats a variable match beats a keyword match, and a label
    // that starts with the query beats one that merely contains it.
    let score = 0;
    for (const t of terms) {
      if (label.startsWith(t)) score += 100;
      else if (label.includes(t)) score += 60;
      else if (env.includes(t)) score += 40;
      else if (section.includes(t)) score += 15;
      else score += 10;
    }
    scored.push({ hit: { ...entry, sectionLabel: sectionLabel.get(entry.section) ?? entry.section }, score });
  }

  return scored
    .sort((a, b) => b.score - a.score || a.hit.label.localeCompare(b.hit.label))
    .map((s) => s.hit);
}

/** How many of a section's settings are overridden, for the rail's badge. */
export function overrideCounts(overridden: Iterable<string>): Record<string, number> {
  const section = new Map(SETTINGS.map((s) => [s.name, s.section]));
  const out: Record<string, number> = {};
  for (const name of overridden) {
    const id = section.get(name);
    if (!id) continue;
    out[id] = (out[id] ?? 0) + 1;
  }
  return out;
}
