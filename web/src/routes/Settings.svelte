<script lang="ts">
  import { api, type Settings, type SettingsPatch, type PruneResult } from "$lib/api/client";
  import { bytes, duration } from "$lib/format";
  import { Button } from "$lib/components/ui/button";

  // The environment is the baseline; anything edited here is stored as an
  // override on top of it. That is why every field can say where its value
  // came from, and why "Use the environment value" is a button rather than a
  // matter of typing the old number back in.
  let settings = $state<Settings | null>(null);
  let error = $state<string | null>(null);
  let notice = $state<string | null>(null);
  let saving = $state(false);
  let pruning = $state(false);
  let pruned = $state<PruneResult | null>(null);

  // The form's working copy. Kept separate from `settings` so a field being
  // typed into is not overwritten by a background refresh.
  type Draft = {
    snapshot_interval_ms: number;
    retention_days: number;
    unchanged_retention_days: number;
    event_retention_days: number;
    retention_interval_ms: number;
    vacuum_interval_ms: number;
    keep_keys: string;
    base_url: string;
    log_level: string;
    notify_on: string;
    notify_min_severity: string;
  };
  let draft = $state<Draft | null>(null);
  // Write-only fields. They are never returned by the API, so they start empty
  // every time and only travel when someone types into them.
  let notifyUrls = $state("");
  let ingestToken = $state("");

  function toDraft(s: Settings): Draft {
    const e = s.effective;
    return {
      snapshot_interval_ms: e.snapshot_interval_ms,
      retention_days: e.retention_days,
      unchanged_retention_days: e.unchanged_retention_days,
      event_retention_days: e.event_retention_days,
      retention_interval_ms: e.retention_interval_ms,
      vacuum_interval_ms: e.vacuum_interval_ms,
      keep_keys: e.keep_keys.join(", "),
      base_url: e.base_url,
      log_level: e.log_level,
      notify_on: e.notify_on.join(", "),
      notify_min_severity: e.notify_min_severity,
    };
  }

  function adopt(s: Settings) {
    settings = s;
    draft = toDraft(s);
    notifyUrls = "";
    ingestToken = "";
  }

  $effect(() => {
    const controller = new AbortController();
    api
      .settings(controller.signal)
      .then((s) => {
        adopt(s);
        error = null;
      })
      .catch((err: Error) => {
        if (err.name !== "AbortError") error = err.message;
      });
    return () => controller.abort();
  });

  const overridden = $derived(new Set(settings?.overridden ?? []));

  function list(value: string): string[] {
    return value
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);
  }

  function lines(value: string): string[] {
    return value
      .split(/[\n,]/)
      .map((s) => s.trim())
      .filter(Boolean);
  }

  // Only what actually differs from what is in force is sent. A patch that
  // restated every field would turn every save into thirteen overrides, and
  // the whole point of the baseline is that most fields never leave it.
  function buildPatch(): SettingsPatch {
    const patch: SettingsPatch = {};
    if (!settings || !draft) return patch;
    const e = settings.effective;

    if (draft.snapshot_interval_ms !== e.snapshot_interval_ms)
      patch.snapshot_interval_ms = Number(draft.snapshot_interval_ms);
    if (draft.retention_days !== e.retention_days) patch.retention_days = Number(draft.retention_days);
    if (draft.unchanged_retention_days !== e.unchanged_retention_days)
      patch.unchanged_retention_days = Number(draft.unchanged_retention_days);
    if (draft.event_retention_days !== e.event_retention_days)
      patch.event_retention_days = Number(draft.event_retention_days);
    if (draft.retention_interval_ms !== e.retention_interval_ms)
      patch.retention_interval_ms = Number(draft.retention_interval_ms);
    if (draft.vacuum_interval_ms !== e.vacuum_interval_ms)
      patch.vacuum_interval_ms = Number(draft.vacuum_interval_ms);
    if (draft.base_url !== e.base_url) patch.base_url = draft.base_url;
    if (draft.log_level !== e.log_level)
      patch.log_level = draft.log_level as SettingsPatch["log_level"];
    if (draft.notify_min_severity !== e.notify_min_severity)
      patch.notify_min_severity = draft.notify_min_severity as SettingsPatch["notify_min_severity"];

    const keep = list(draft.keep_keys);
    if (keep.join(",") !== e.keep_keys.join(",")) patch.keep_keys = keep;
    const on = list(draft.notify_on);
    if (on.join(",") !== e.notify_on.join(",")) patch.notify_on = on;

    if (notifyUrls.trim() !== "") patch.notify_urls = lines(notifyUrls);
    if (ingestToken.trim() !== "") patch.ingest_token = ingestToken.trim();
    return patch;
  }

  const dirty = $derived.by(() => {
    void [draft, settings, notifyUrls, ingestToken];
    return Object.keys(buildPatch()).length > 0;
  });

  async function save() {
    const patch = buildPatch();
    if (Object.keys(patch).length === 0) return;
    saving = true;
    try {
      adopt(await api.updateSettings(patch));
      error = null;
      notice = "Saved. Changes are in force now — no restart needed.";
    } catch (err) {
      error = (err as Error).message;
      notice = null;
    } finally {
      saving = false;
    }
  }

  async function useEnvironment(field: string) {
    saving = true;
    try {
      adopt(await api.updateSettings({ reset: [field] }));
      error = null;
      notice = null;
    } catch (err) {
      error = (err as Error).message;
    } finally {
      saving = false;
    }
  }

  async function resetAll() {
    saving = true;
    try {
      adopt(await api.resetSettings());
      error = null;
      notice = "Every override dropped. Silt is running exactly what its environment says.";
    } catch (err) {
      error = (err as Error).message;
    } finally {
      saving = false;
    }
  }

  function revert() {
    if (settings) adopt(settings);
    notice = null;
  }

  async function prune() {
    pruning = true;
    try {
      pruned = await api.prune();
      settings = await api.settings();
      error = null;
    } catch (err) {
      error = (err as Error).message;
    } finally {
      pruning = false;
    }
  }

  const MINUTE = 60_000;
  const HOUR = 3_600_000;
  const DAY = 86_400_000;

  const INTERVALS = [
    { label: "1 minute", ms: MINUTE },
    { label: "5 minutes", ms: 5 * MINUTE },
    { label: "15 minutes", ms: 15 * MINUTE },
    { label: "30 minutes", ms: 30 * MINUTE },
    { label: "1 hour", ms: HOUR },
    { label: "6 hours", ms: 6 * HOUR },
  ];
  const RETENTION_INTERVALS = [
    { label: "15 minutes", ms: 15 * MINUTE },
    { label: "1 hour", ms: HOUR },
    { label: "6 hours", ms: 6 * HOUR },
    { label: "24 hours", ms: DAY },
  ];
  const VACUUM_INTERVALS = [
    { label: "disabled", ms: 0 },
    { label: "weekly", ms: 7 * DAY },
    { label: "monthly", ms: 30 * DAY },
  ];

  const inputClass =
    "w-full rounded-md border border-border bg-background px-2.5 py-1.5 text-sm outline-none focus:ring-2 focus:ring-ring";
</script>

<div class="space-y-10">
  <header class="flex flex-wrap items-end justify-between gap-4">
    <div>
      <h2 class="text-2xl font-semibold tracking-tight">Settings</h2>
      <p class="mt-1 max-w-2xl text-sm text-muted-foreground">
        Your environment variables are the baseline. Anything changed here is stored on top of them
        and takes effect immediately — no container recreate. Fields marked
        <span class="mx-0.5 rounded bg-secondary px-1 py-0.5 text-[10px] uppercase tracking-wide">set here</span>
        are no longer following the environment.
      </p>
    </div>
    {#if settings && overridden.size > 0}
      <Button variant="outline" size="sm" onclick={resetAll} disabled={saving}>
        Use the environment for everything
      </Button>
    {/if}
  </header>

  {#if error}
    <p class="rounded border border-red-900/60 bg-red-950/40 px-4 py-3 text-sm text-red-300">{error}</p>
  {/if}
  {#if notice}
    <p class="rounded border border-emerald-900/60 bg-emerald-950/30 px-4 py-3 text-sm text-emerald-300">
      {notice}
    </p>
  {/if}

  {#if settings && draft}
    {#snippet field(name: string, label: string, envVar: string, hint?: string)}
      <div class="flex items-baseline gap-2">
        <label for={name} class="text-sm font-medium">{label}</label>
        {#if overridden.has(name)}
          <span class="rounded bg-secondary px-1 py-0.5 text-[10px] uppercase tracking-wide text-muted-foreground">
            set here
          </span>
          <button
            type="button"
            class="text-[11px] text-muted-foreground underline underline-offset-2 hover:text-foreground"
            onclick={() => useEnvironment(name)}
            disabled={saving}
          >
            use {envVar}
          </button>
        {/if}
        <span class="ml-auto shrink-0 font-mono text-[10px] text-muted-foreground/50">{envVar}</span>
      </div>
      {#if hint}
        <p class="mt-0.5 text-xs text-muted-foreground/70">{hint}</p>
      {/if}
    {/snippet}

    <section class="space-y-5">
      <h3 class="text-xs font-medium uppercase tracking-wide text-muted-foreground">Collection</h3>

      <div class="max-w-md">
        {@render field(
          "snapshot_interval_ms",
          "Reconcile interval",
          "SILT_SNAPSHOT_INTERVAL",
          "Silt records changes as Docker reports them; this is the sweep that catches whatever the event stream missed.",
        )}
        <select id="snapshot_interval_ms" bind:value={draft.snapshot_interval_ms} class="{inputClass} mt-2">
          {#each INTERVALS as option (option.ms)}
            <option value={option.ms}>{option.label}</option>
          {/each}
          {#if !INTERVALS.some((o) => o.ms === draft?.snapshot_interval_ms)}
            <option value={draft.snapshot_interval_ms}>{duration(draft.snapshot_interval_ms)}</option>
          {/if}
        </select>
      </div>

      <div class="max-w-md">
        {@render field(
          "keep_keys",
          "Environment keys kept readable",
          "SILT_KEEP_KEYS",
          "Every environment value is a keyed digest unless its key is on the safe list. These are the extras you added, comma separated; a single leading or trailing * is allowed.",
        )}
        <input id="keep_keys" bind:value={draft.keep_keys} placeholder="PUID, TZ, MY_APP_*" class="{inputClass} mt-2" />
      </div>

      <div class="max-w-md">
        {@render field("log_level", "Log level", "SILT_LOG_LEVEL")}
        <select id="log_level" bind:value={draft.log_level} class="{inputClass} mt-2">
          {#each ["debug", "info", "warn", "error"] as level (level)}
            <option value={level}>{level}</option>
          {/each}
        </select>
      </div>
    </section>

    <section class="space-y-5">
      <h3 class="text-xs font-medium uppercase tracking-wide text-muted-foreground">Retention</h3>
      <p class="-mt-3 max-w-2xl text-xs text-muted-foreground/70">
        Zero means keep forever. Runtime-only snapshots are the proof-of-liveness rows between
        changes, and cannot outlive the changes they sit between.
      </p>

      <div class="grid gap-5 sm:grid-cols-3">
        <div>
          {@render field("retention_days", "Changed snapshots", "SILT_RETENTION_DAYS")}
          <div class="mt-2 flex items-center gap-2">
            <input id="retention_days" type="number" min="0" bind:value={draft.retention_days} class={inputClass} />
            <span class="shrink-0 text-xs text-muted-foreground">days</span>
          </div>
        </div>
        <div>
          {@render field("unchanged_retention_days", "Runtime-only", "SILT_UNCHANGED_RETENTION_DAYS")}
          <div class="mt-2 flex items-center gap-2">
            <input
              id="unchanged_retention_days"
              type="number"
              min="0"
              bind:value={draft.unchanged_retention_days}
              class={inputClass}
            />
            <span class="shrink-0 text-xs text-muted-foreground">days</span>
          </div>
        </div>
        <div>
          {@render field("event_retention_days", "Events", "SILT_EVENT_RETENTION_DAYS")}
          <div class="mt-2 flex items-center gap-2">
            <input
              id="event_retention_days"
              type="number"
              min="0"
              bind:value={draft.event_retention_days}
              class={inputClass}
            />
            <span class="shrink-0 text-xs text-muted-foreground">days</span>
          </div>
        </div>
      </div>

      <div class="grid gap-5 sm:grid-cols-2">
        <div>
          {@render field("retention_interval_ms", "Retention pass runs every", "SILT_RETENTION_INTERVAL")}
          <select id="retention_interval_ms" bind:value={draft.retention_interval_ms} class="{inputClass} mt-2">
            {#each RETENTION_INTERVALS as option (option.ms)}
              <option value={option.ms}>{option.label}</option>
            {/each}
            {#if !RETENTION_INTERVALS.some((o) => o.ms === draft?.retention_interval_ms)}
              <option value={draft.retention_interval_ms}>{duration(draft.retention_interval_ms)}</option>
            {/if}
          </select>
        </div>
        <div>
          {@render field(
            "vacuum_interval_ms",
            "Vacuum",
            "SILT_VACUUM_INTERVAL",
            "Reclaims free pages by rewriting the whole file. Cheap to skip, expensive to run.",
          )}
          <select id="vacuum_interval_ms" bind:value={draft.vacuum_interval_ms} class="{inputClass} mt-2">
            {#each VACUUM_INTERVALS as option (option.ms)}
              <option value={option.ms}>{option.label}</option>
            {/each}
            {#if !VACUUM_INTERVALS.some((o) => o.ms === draft?.vacuum_interval_ms)}
              <option value={draft.vacuum_interval_ms}>{duration(draft.vacuum_interval_ms)}</option>
            {/if}
          </select>
        </div>
      </div>
    </section>

    <section class="space-y-5">
      <h3 class="text-xs font-medium uppercase tracking-wide text-muted-foreground">Notifications</h3>

      <div class="max-w-2xl">
        {@render field("notify_urls", "Targets", "SILT_NOTIFY_URLS")}
        <p class="mt-0.5 text-xs text-muted-foreground/70">
          shoutrrr URLs, one per line. A shoutrrr URL carries the credential for the service it points
          at, so Silt shows you what is configured but never hands the URL back. Typing here replaces
          the whole list.
        </p>
        {#if settings.effective.notify_targets.length > 0}
          <ul class="mt-2 space-y-1">
            {#each settings.effective.notify_targets as target, i (i)}
              <li class="font-mono text-xs text-muted-foreground">{target}</li>
            {/each}
          </ul>
        {:else}
          <p class="mt-2 font-mono text-xs text-muted-foreground">none configured</p>
        {/if}
        <textarea
          id="notify_urls"
          bind:value={notifyUrls}
          rows="3"
          placeholder="gotify://gotify.example.com/AppToken&#10;discord://token@id"
          class="{inputClass} mt-2 font-mono text-xs"
        ></textarea>
      </div>

      <div class="grid max-w-2xl gap-5 sm:grid-cols-2">
        <div>
          {@render field("notify_on", "Notify on", "SILT_NOTIFY_ON")}
          <p class="mt-0.5 text-xs text-muted-foreground/70">
            Change kinds, comma separated, or <span class="font-mono">all</span>.
          </p>
          <input id="notify_on" bind:value={draft.notify_on} class="{inputClass} mt-2 font-mono text-xs" />
        </div>
        <div>
          {@render field("notify_min_severity", "Minimum severity", "SILT_NOTIFY_MIN_SEVERITY")}
          <p class="mt-0.5 text-xs text-muted-foreground/70">ANDed with the kinds above.</p>
          <select id="notify_min_severity" bind:value={draft.notify_min_severity} class="{inputClass} mt-2">
            {#each ["low", "medium", "high"] as level (level)}
              <option value={level}>{level}</option>
            {/each}
          </select>
        </div>
      </div>

      <div class="max-w-md">
        {@render field(
          "base_url",
          "Base URL",
          "SILT_BASE_URL",
          "Where Silt is reachable, used to build the link in a notification. Empty omits the link.",
        )}
        <input id="base_url" bind:value={draft.base_url} placeholder="https://silt.example.lan" class="{inputClass} mt-2" />
      </div>
    </section>

    <section class="space-y-5">
      <h3 class="text-xs font-medium uppercase tracking-wide text-muted-foreground">Ingest webhook</h3>

      <div class="max-w-md">
        {@render field("ingest_token", "Token", "SILT_INGEST_TOKEN")}
        <p class="mt-0.5 text-xs text-muted-foreground/70">
          Guards <span class="font-mono">POST /api/ingest</span>. Currently
          <strong>{settings.effective.ingest_configured ? "configured" : "not configured"}</strong>; the
          endpoint refuses every request while no token is set. Typing here replaces it.
        </p>
        <input
          id="ingest_token"
          type="password"
          autocomplete="new-password"
          bind:value={ingestToken}
          placeholder={settings.effective.ingest_configured ? "•••••••• — type to replace" : "not configured"}
          class="{inputClass} mt-2 font-mono text-xs"
        />
        {#if settings.effective.ingest_configured}
          <button
            type="button"
            class="mt-2 text-[11px] text-muted-foreground underline underline-offset-2 hover:text-foreground"
            onclick={() => api.updateSettings({ ingest_token: "" }).then(adopt).catch((e) => (error = e.message))}
            disabled={saving}
          >
            Turn the ingest endpoint off
          </button>
        {/if}
      </div>
    </section>

    <!-- Sticky rather than at the bottom of the page: this form is longer than
         a screen, and a save button you have to scroll to find is the same
         complaint as a settings link you have to scroll to find. -->
    <div
      class="sticky bottom-0 -mx-6 flex items-center gap-3 border-t border-border bg-background/95 px-6 py-3 backdrop-blur-sm"
    >
      <Button size="sm" onclick={save} disabled={!dirty || saving}>
        {saving ? "Saving…" : "Save changes"}
      </Button>
      {#if dirty}
        <Button variant="ghost" size="sm" onclick={revert} disabled={saving}>Discard</Button>
        <span class="text-xs text-muted-foreground">Unsaved changes</span>
      {:else}
        <span class="text-xs text-muted-foreground">
          {overridden.size === 0
            ? "Running exactly what the environment says."
            : `${overridden.size} setting${overridden.size === 1 ? "" : "s"} overridden here.`}
        </span>
      {/if}
    </div>

    <section>
      <h3 class="text-xs font-medium uppercase tracking-wide text-muted-foreground">
        Environment only
      </h3>
      <p class="mt-1 max-w-2xl text-xs text-muted-foreground/70">
        These cannot be changed here. Some need a restart to take effect; the rest are the boundary
        protecting this screen — a UI that could widen which files Silt reads, or turn off the login
        in front of it, would be a way in rather than a setting.
      </p>
      <dl class="mt-3 divide-y divide-border border-y border-border">
        {#each [["Host name", settings.fixed.host_name, "SILT_HOST_NAME"], ["Docker endpoint", settings.fixed.docker_host, "SILT_DOCKER_HOST"], ["Database", settings.fixed.db_path, "SILT_DB_PATH"], ["Listen address", settings.fixed.listen_addr, "SILT_LISTEN_ADDR"], ["Authentication", settings.fixed.auth_mode, "SILT_TRUST_PROXY_AUTH / SILT_PASSWORD_HASH"], ["Compose roots", settings.fixed.compose_roots.join(", ") || "none — file capture is off", "SILT_COMPOSE_ROOTS"], ["Max compose file", bytes(settings.fixed.max_compose_file_bytes), "SILT_MAX_COMPOSE_FILE_BYTES"]] as [label, value, envVar] (envVar)}
          <div class="flex items-baseline gap-3 py-2 text-sm">
            <dt class="w-48 shrink-0">{label}</dt>
            <!-- min-w-0 lets a long value (a database path) wrap inside its own
                 column instead of pushing the variable name onto a new line. -->
            <dd class="min-w-0 flex-1 break-all font-mono text-xs">{value}</dd>
            <dd class="hidden shrink-0 font-mono text-xs text-muted-foreground/50 lg:block">{envVar}</dd>
          </div>
        {/each}
      </dl>
    </section>

    <section>
      <h3 class="text-xs font-medium uppercase tracking-wide text-muted-foreground">Storage</h3>
      <dl class="mt-3 flex flex-wrap gap-8 text-sm">
        <div>
          <dt class="text-xs text-muted-foreground">Blobs</dt>
          <dd class="font-mono">{settings.usage.blobs}</dd>
        </div>
        <div>
          <dt class="text-xs text-muted-foreground">Stored</dt>
          <dd class="font-mono">{bytes(settings.usage.stored_bytes)}</dd>
        </div>
        <div>
          <dt class="text-xs text-muted-foreground">Uncompressed</dt>
          <dd class="font-mono">{bytes(settings.usage.uncompressed_bytes)}</dd>
        </div>
        <div>
          <dt class="text-xs text-muted-foreground">Events</dt>
          <dd class="font-mono">{settings.usage.events}</dd>
        </div>
      </dl>

      <div class="mt-4">
        <Button variant="outline" size="sm" onclick={prune} disabled={pruning}>
          {pruning ? "Pruning…" : "Run retention pass now"}
        </Button>
      </div>

      {#if pruned}
        <p class="mt-2 text-xs text-muted-foreground">
          Removed {pruned.unchanged_snapshots} runtime-only and {pruned.changed_snapshots} changed
          snapshots, {pruned.events} events, {pruned.blobs} blobs.
        </p>
      {/if}
    </section>

    <p class="text-xs text-muted-foreground/50">
      Silt {settings.release} · build {settings.version}
    </p>
  {:else if !error}
    <p class="text-sm text-muted-foreground">Loading…</p>
  {/if}
</div>
