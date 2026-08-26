<script lang="ts">
  import { api, type Settings, type SettingsPatch, type PruneResult } from "$lib/api/client";
  import { bytes, duration, sampleDate } from "$lib/format";
  import { prefs, type Clock, type DateStyle, type Layout, type TimeStamps } from "$lib/prefs.svelte";
  import { Button } from "$lib/components/ui/button";
  import type { Snippet } from "svelte";

  // The environment is the baseline; anything edited here is stored as an
  // override on top of it. That is why every field can say where its value
  // came from, and why "use the environment value" is a button rather than a
  // matter of typing the old number back in.
  //
  // The sections are a left rail rather than one long scroll: the previous
  // version put a save button and seven headings on a page taller than the
  // window, which is the same complaint as a settings link at the bottom of a
  // thirty-item list.
  let settings = $state<Settings | null>(null);
  let error = $state<string | null>(null);
  let notice = $state<string | null>(null);
  let saving = $state(false);
  let pruning = $state(false);
  let pruned = $state<PruneResult | null>(null);

  const SECTIONS = [
    { id: "appearance", label: "Appearance" },
    { id: "collection", label: "Collection" },
    { id: "retention", label: "Retention" },
    { id: "notifications", label: "Notifications" },
    { id: "ingest", label: "Ingest webhook" },
    { id: "environment", label: "Environment only" },
    { id: "storage", label: "Storage" },
  ];
  let section = $state("appearance");

  // The form's working copy, kept separate from `settings` so a field being
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
  let draft = $state<Draft>(emptyDraft());
  // Write-only fields. They are never returned by the API, so they start empty
  // every time and only travel when someone types into them.
  let notifyUrls = $state("");
  let ingestToken = $state("");

  // A non-null starting value rather than null: every control below lives in a
  // snippet, and a snippet is a hoisted function that no {#if} can narrow
  // into. Rendering is gated on `settings` instead.
  function emptyDraft(): Draft {
    return {
      snapshot_interval_ms: 300_000,
      retention_days: 365,
      unchanged_retention_days: 7,
      event_retention_days: 90,
      retention_interval_ms: 3_600_000,
      vacuum_interval_ms: 0,
      keep_keys: "",
      base_url: "",
      log_level: "info",
      notify_on: "",
      notify_min_severity: "medium",
    };
  }

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
    return value.split(",").map((s) => s.trim()).filter(Boolean);
  }

  function multiline(value: string): string[] {
    return value.split(/[\n,]/).map((s) => s.trim()).filter(Boolean);
  }

  // Only what actually differs from what is in force is sent. A patch that
  // restated every field would turn every save into thirteen overrides, and
  // the whole point of the baseline is that most fields never leave it.
  function buildPatch(): SettingsPatch {
    const patch: SettingsPatch = {};
    if (!settings) return patch;
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
    if (draft.log_level !== e.log_level) patch.log_level = draft.log_level as SettingsPatch["log_level"];
    if (draft.notify_min_severity !== e.notify_min_severity)
      patch.notify_min_severity = draft.notify_min_severity as SettingsPatch["notify_min_severity"];

    const keep = list(draft.keep_keys);
    if (keep.join(",") !== e.keep_keys.join(",")) patch.keep_keys = keep;
    const on = list(draft.notify_on);
    if (on.join(",") !== e.notify_on.join(",")) patch.notify_on = on;

    if (notifyUrls.trim() !== "") patch.notify_urls = multiline(notifyUrls);
    if (ingestToken.trim() !== "") patch.ingest_token = ingestToken.trim();
    return patch;
  }

  const dirty = $derived.by(() => {
    void [draft, settings, notifyUrls, ingestToken];
    return Object.keys(buildPatch()).length > 0;
  });

  async function apply(fn: () => Promise<Settings>, message?: string) {
    saving = true;
    try {
      adopt(await fn());
      error = null;
      notice = message ?? null;
    } catch (err) {
      error = (err as Error).message;
      notice = null;
    } finally {
      saving = false;
    }
  }

  const save = () => {
    const patch = buildPatch();
    if (Object.keys(patch).length === 0) return;
    return apply(() => api.updateSettings(patch), "Saved. In force now — no restart needed.");
  };
  const useEnvironment = (field: string) => apply(() => api.updateSettings({ reset: [field] }));
  const resetAll = () =>
    apply(() => api.resetSettings(), "Every override dropped. Silt is running what its environment says.");

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
    ["1 minute", MINUTE],
    ["5 minutes", 5 * MINUTE],
    ["15 minutes", 15 * MINUTE],
    ["30 minutes", 30 * MINUTE],
    ["1 hour", HOUR],
    ["6 hours", 6 * HOUR],
  ] as const;
  const RETENTION_INTERVALS = [
    ["15 minutes", 15 * MINUTE],
    ["1 hour", HOUR],
    ["6 hours", 6 * HOUR],
    ["24 hours", DAY],
  ] as const;
  const VACUUM_INTERVALS = [
    ["disabled", 0],
    ["weekly", 7 * DAY],
    ["monthly", 30 * DAY],
  ] as const;

  const input =
    "w-full rounded-md border border-border bg-background px-2.5 py-1.5 text-sm outline-none focus:ring-2 focus:ring-ring";

  // A live sample, so a date order is chosen by looking at it rather than by
  // decoding "dmy".
  const dateSample = $derived((style: DateStyle) => sampleDate(style, prefs.clock));
</script>

{#snippet field(name: string, label: string, envVar: string, hint: string | undefined, control: Snippet)}
  <div class="grid gap-1.5 py-3.5 sm:grid-cols-[15rem_1fr] sm:gap-6">
    <div class="min-w-0">
      <div class="flex flex-wrap items-baseline gap-x-2 gap-y-1">
        <label for={name} class="text-sm font-medium">{label}</label>
        {#if overridden.has(name)}
          <span class="rounded bg-secondary px-1 py-0.5 text-[10px] uppercase tracking-wide text-muted-foreground">
            set here
          </span>
        {/if}
      </div>
      {#if hint}
        <p class="mt-1 text-xs leading-relaxed text-muted-foreground/70">{hint}</p>
      {/if}
      <p class="mt-1 font-mono text-[10px] text-muted-foreground/40">{envVar}</p>
      {#if overridden.has(name)}
        <button
          type="button"
          class="mt-1 text-[11px] text-muted-foreground underline underline-offset-2 hover:text-foreground"
          onclick={() => useEnvironment(name)}
          disabled={saving}
        >
          use the environment value
        </button>
      {/if}
    </div>
    <div class="min-w-0 max-w-md">{@render control()}</div>
  </div>
{/snippet}

{#snippet choice(name: string, label: string, hint: string | undefined, control: Snippet)}
  <div class="grid gap-1.5 py-3.5 sm:grid-cols-[15rem_1fr] sm:gap-6">
    <div class="min-w-0">
      <label for={name} class="text-sm font-medium">{label}</label>
      {#if hint}
        <p class="mt-1 text-xs leading-relaxed text-muted-foreground/70">{hint}</p>
      {/if}
    </div>
    <div class="min-w-0 max-w-md">{@render control()}</div>
  </div>
{/snippet}

<div class="flex flex-col gap-6 lg:flex-row">
  <!-- The section rail. Sticky rather than scrolling with the pane, so you can
       always get from Retention to Storage without scrolling back up. -->
  <nav class="shrink-0 lg:sticky lg:top-0 lg:w-52 lg:self-start" aria-label="Settings sections">
    <h2 class="mb-3 hidden text-lg font-semibold tracking-tight lg:block">Settings</h2>
    <div class="flex gap-1 overflow-x-auto pb-1 lg:flex-col lg:overflow-visible lg:pb-0">
      {#each SECTIONS as s (s.id)}
        <button
          type="button"
          class="shrink-0 whitespace-nowrap rounded-md px-2.5 py-1.5 text-left text-sm transition-colors
                 {section === s.id
            ? 'bg-secondary text-secondary-foreground'
            : 'text-muted-foreground hover:bg-secondary/50 hover:text-foreground'}"
          onclick={() => (section = s.id)}
        >
          {s.label}
        </button>
      {/each}
    </div>
    {#if settings && overridden.size > 0}
      <button
        type="button"
        class="mt-4 hidden text-[11px] text-muted-foreground underline underline-offset-2 hover:text-foreground lg:block"
        onclick={resetAll}
        disabled={saving}
      >
        Use the environment for everything
      </button>
    {/if}
  </nav>

  <div class="min-w-0 flex-1 space-y-4">
    {#if error}
      <p class="rounded-md border border-red-500/40 bg-red-500/10 px-4 py-2.5 text-sm text-red-500 dark:text-red-300">
        {error}
      </p>
    {/if}
    {#if notice}
      <p class="rounded-md border border-emerald-500/40 bg-emerald-500/10 px-4 py-2.5 text-sm text-emerald-600 dark:text-emerald-300">
        {notice}
      </p>
    {/if}

    {#if section === "appearance"}
      <section>
        <h3 class="text-sm font-semibold">Appearance</h3>
        <p class="mt-1 max-w-2xl text-xs leading-relaxed text-muted-foreground">
          These live in this browser, not in Silt. A 24-hour clock or a dd/mm/yyyy date is a
          property of whoever is reading the screen, not of the install, so two people looking at
          the same Silt each get their own.
        </p>

        <div class="mt-2 divide-y divide-border">
          {#snippet layoutControl()}
            <div class="flex rounded-md border border-border">
              {#each [["top", "Top bar"], ["side", "Left rail"]] as [value, label] (value)}
                <button
                  type="button"
                  class="flex-1 px-3 py-1.5 text-xs transition-colors first:rounded-l-md last:rounded-r-md
                         {prefs.layout === value
                    ? 'bg-secondary text-secondary-foreground'
                    : 'text-muted-foreground hover:text-foreground'}"
                  onclick={() => prefs.set("layout", value as Layout)}
                >
                  {label}
                </button>
              {/each}
            </div>
          {/snippet}
          {@render choice(
            "layout",
            "Navigation",
            "Sections across the top, or stacked in the left rail above the project list.",
            layoutControl,
          )}

          {#snippet clockControl()}
            <select id="clock" class={input} value={prefs.clock} onchange={(e) => prefs.set("clock", e.currentTarget.value as Clock)}>
              <option value="system">Follow this device</option>
              <option value="h24">24-hour (14:30)</option>
              <option value="h12">12-hour (2:30 PM)</option>
            </select>
          {/snippet}
          {@render choice("clock", "Clock", undefined, clockControl)}

          {#snippet dateControl()}
            <select
              id="dateStyle"
              class={input}
              value={prefs.dateStyle}
              onchange={(e) => prefs.set("dateStyle", e.currentTarget.value as DateStyle)}
            >
              <option value="system">Follow this device — {dateSample("system")}</option>
              <option value="dmy">Day first — {dateSample("dmy")}</option>
              <option value="mdy">Month first — {dateSample("mdy")}</option>
              <option value="ymd">Year first — {dateSample("ymd")}</option>
            </select>
          {/snippet}
          {@render choice("dateStyle", "Date order", undefined, dateControl)}

          {#snippet stampControl()}
            <select
              id="timestamps"
              class={input}
              value={prefs.timestamps}
              onchange={(e) => prefs.set("timestamps", e.currentTarget.value as TimeStamps)}
            >
              <option value="relative">Relative — 3m ago</option>
              <option value="absolute">Absolute — {dateSample(prefs.dateStyle)}</option>
            </select>
            <label class="mt-2 flex items-center gap-2 text-xs text-muted-foreground">
              <input
                type="checkbox"
                checked={prefs.seconds}
                onchange={(e) => prefs.set("seconds", e.currentTarget.checked)}
                class="accent-emerald-500"
              />
              Show seconds
            </label>
          {/snippet}
          {@render choice(
            "timestamps",
            "Timestamps",
            "Relative reads faster on a live page; absolute is what you want when you are lining Silt up against another tool's logs. The other form is always in the tooltip.",
            stampControl,
          )}
        </div>
      </section>
    {/if}

    {#if settings}
      {#if section === "collection"}
        <section>
          <h3 class="text-sm font-semibold">Collection</h3>
          <div class="mt-2 divide-y divide-border">
            {#snippet intervalControl()}
              <select id="snapshot_interval_ms" bind:value={draft.snapshot_interval_ms} class={input}>
                {#each INTERVALS as [label, ms] (ms)}
                  <option value={ms}>{label}</option>
                {/each}
                {#if !INTERVALS.some(([, ms]) => ms === draft.snapshot_interval_ms)}
                  <option value={draft.snapshot_interval_ms}>{duration(draft.snapshot_interval_ms)}</option>
                {/if}
              </select>
            {/snippet}
            {@render field(
              "snapshot_interval_ms",
              "Reconcile interval",
              "SILT_SNAPSHOT_INTERVAL",
              "Silt records changes as Docker reports them; this is the sweep that catches whatever the event stream missed.",
              intervalControl,
            )}

            {#snippet keepControl()}
              <input id="keep_keys" bind:value={draft.keep_keys} placeholder="PUID, TZ, MY_APP_*" class={input} />
            {/snippet}
            {@render field(
              "keep_keys",
              "Keys kept readable",
              "SILT_KEEP_KEYS",
              "Every environment value is a keyed digest unless its key is on the safe list. These are the extras you added, comma separated; one leading or trailing * is allowed.",
              keepControl,
            )}

            {#snippet logControl()}
              <select id="log_level" bind:value={draft.log_level} class={input}>
                {#each ["debug", "info", "warn", "error"] as level (level)}
                  <option value={level}>{level}</option>
                {/each}
              </select>
            {/snippet}
            {@render field("log_level", "Log level", "SILT_LOG_LEVEL", undefined, logControl)}
          </div>
        </section>
      {/if}

      {#if section === "retention"}
        <section>
          <h3 class="text-sm font-semibold">Retention</h3>
          <p class="mt-1 max-w-2xl text-xs leading-relaxed text-muted-foreground">
            Zero means keep forever. Runtime-only snapshots are the proof-of-liveness rows between
            changes, and cannot outlive the changes they sit between.
          </p>
          <div class="mt-2 divide-y divide-border">
            {#snippet changedControl()}
              <div class="flex items-center gap-2">
                <input id="retention_days" type="number" min="0" bind:value={draft.retention_days} class={input} />
                <span class="shrink-0 text-xs text-muted-foreground">days</span>
              </div>
            {/snippet}
            {@render field("retention_days", "Changed snapshots", "SILT_RETENTION_DAYS", undefined, changedControl)}

            {#snippet unchangedControl()}
              <div class="flex items-center gap-2">
                <input
                  id="unchanged_retention_days"
                  type="number"
                  min="0"
                  bind:value={draft.unchanged_retention_days}
                  class={input}
                />
                <span class="shrink-0 text-xs text-muted-foreground">days</span>
              </div>
            {/snippet}
            {@render field(
              "unchanged_retention_days",
              "Runtime-only snapshots",
              "SILT_UNCHANGED_RETENTION_DAYS",
              undefined,
              unchangedControl,
            )}

            {#snippet eventControl()}
              <div class="flex items-center gap-2">
                <input
                  id="event_retention_days"
                  type="number"
                  min="0"
                  bind:value={draft.event_retention_days}
                  class={input}
                />
                <span class="shrink-0 text-xs text-muted-foreground">days</span>
              </div>
            {/snippet}
            {@render field("event_retention_days", "Events", "SILT_EVENT_RETENTION_DAYS", undefined, eventControl)}

            {#snippet passControl()}
              <select id="retention_interval_ms" bind:value={draft.retention_interval_ms} class={input}>
                {#each RETENTION_INTERVALS as [label, ms] (ms)}
                  <option value={ms}>{label}</option>
                {/each}
                {#if !RETENTION_INTERVALS.some(([, ms]) => ms === draft.retention_interval_ms)}
                  <option value={draft.retention_interval_ms}>{duration(draft.retention_interval_ms)}</option>
                {/if}
              </select>
            {/snippet}
            {@render field(
              "retention_interval_ms",
              "Retention pass runs every",
              "SILT_RETENTION_INTERVAL",
              undefined,
              passControl,
            )}

            {#snippet vacuumControl()}
              <select id="vacuum_interval_ms" bind:value={draft.vacuum_interval_ms} class={input}>
                {#each VACUUM_INTERVALS as [label, ms] (ms)}
                  <option value={ms}>{label}</option>
                {/each}
                {#if !VACUUM_INTERVALS.some(([, ms]) => ms === draft.vacuum_interval_ms)}
                  <option value={draft.vacuum_interval_ms}>{duration(draft.vacuum_interval_ms)}</option>
                {/if}
              </select>
            {/snippet}
            {@render field(
              "vacuum_interval_ms",
              "Vacuum",
              "SILT_VACUUM_INTERVAL",
              "Reclaims free pages by rewriting the whole file. Cheap to skip, expensive to run.",
              vacuumControl,
            )}
          </div>
        </section>
      {/if}

      {#if section === "notifications"}
        <section>
          <h3 class="text-sm font-semibold">Notifications</h3>
          <div class="mt-2 divide-y divide-border">
            {#snippet targetsControl()}
              {#if (settings?.effective.notify_targets.length ?? 0) > 0}
                <ul class="mb-2 space-y-1">
                  {#each settings?.effective.notify_targets ?? [] as target, i (i)}
                    <li class="font-mono text-xs text-muted-foreground">{target}</li>
                  {/each}
                </ul>
              {:else}
                <p class="mb-2 font-mono text-xs text-muted-foreground">none configured</p>
              {/if}
              <textarea
                id="notify_urls"
                bind:value={notifyUrls}
                rows="3"
                placeholder="gotify://gotify.example.com/AppToken&#10;discord://token@id"
                class="{input} font-mono text-xs"
              ></textarea>
            {/snippet}
            {@render field(
              "notify_urls",
              "Targets",
              "SILT_NOTIFY_URLS",
              "shoutrrr URLs, one per line. A shoutrrr URL carries the credential for the service it points at, so Silt shows what is configured but never hands the URL back. Typing here replaces the whole list.",
              targetsControl,
            )}

            {#snippet kindsControl()}
              <input id="notify_on" bind:value={draft.notify_on} class="{input} font-mono text-xs" />
            {/snippet}
            {@render field(
              "notify_on",
              "Notify on",
              "SILT_NOTIFY_ON",
              "Change kinds, comma separated, or `all`.",
              kindsControl,
            )}

            {#snippet severityControl()}
              <select id="notify_min_severity" bind:value={draft.notify_min_severity} class={input}>
                {#each ["low", "medium", "high"] as level (level)}
                  <option value={level}>{level}</option>
                {/each}
              </select>
            {/snippet}
            {@render field(
              "notify_min_severity",
              "Minimum severity",
              "SILT_NOTIFY_MIN_SEVERITY",
              "ANDed with the kinds above.",
              severityControl,
            )}

            {#snippet baseControl()}
              <input id="base_url" bind:value={draft.base_url} placeholder="https://silt.example.lan" class={input} />
            {/snippet}
            {@render field(
              "base_url",
              "Base URL",
              "SILT_BASE_URL",
              "Where Silt is reachable, used to build the link in a notification. Empty omits the link.",
              baseControl,
            )}
          </div>
        </section>
      {/if}

      {#if section === "ingest"}
        <section>
          <h3 class="text-sm font-semibold">Ingest webhook</h3>
          <div class="mt-2 divide-y divide-border">
            {#snippet tokenControl()}
              <input
                id="ingest_token"
                type="password"
                autocomplete="new-password"
                bind:value={ingestToken}
                placeholder={settings?.effective.ingest_configured ? "•••••••• — type to replace" : "not configured"}
                class="{input} font-mono text-xs"
              />
              {#if settings?.effective.ingest_configured}
                <button
                  type="button"
                  class="mt-2 text-[11px] text-muted-foreground underline underline-offset-2 hover:text-foreground"
                  onclick={() => apply(() => api.updateSettings({ ingest_token: "" }))}
                  disabled={saving}
                >
                  Turn the ingest endpoint off
                </button>
              {/if}
            {/snippet}
            {@render field(
              "ingest_token",
              "Token",
              "SILT_INGEST_TOKEN",
              settings.effective.ingest_configured
                ? "Guards POST /api/ingest, and is configured. Typing here replaces it."
                : "Guards POST /api/ingest. Not configured, so the endpoint refuses every request.",
              tokenControl,
            )}
          </div>
        </section>
      {/if}

      {#if section === "environment"}
        <section>
          <h3 class="text-sm font-semibold">Environment only</h3>
          <p class="mt-1 max-w-2xl text-xs leading-relaxed text-muted-foreground">
            These cannot be changed here. Some need a restart to take effect; the rest are the
            boundary protecting this screen — a UI that could widen which files Silt reads, or turn
            off the login in front of it, would be a way in rather than a setting.
          </p>
          <dl class="mt-3 divide-y divide-border">
            {#each [["Host name", settings.fixed.host_name, "SILT_HOST_NAME"], ["Docker endpoint", settings.fixed.docker_host, "SILT_DOCKER_HOST"], ["Database", settings.fixed.db_path, "SILT_DB_PATH"], ["Listen address", settings.fixed.listen_addr, "SILT_LISTEN_ADDR"], ["Authentication", settings.fixed.auth_mode, "SILT_TRUST_PROXY_AUTH / SILT_PASSWORD_HASH"], ["Compose roots", settings.fixed.compose_roots.join(", ") || "none — file capture is off", "SILT_COMPOSE_ROOTS"], ["Max compose file", bytes(settings.fixed.max_compose_file_bytes), "SILT_MAX_COMPOSE_FILE_BYTES"]] as [label, value, envVar] (envVar)}
              <div class="grid gap-1 py-2.5 text-sm sm:grid-cols-[15rem_1fr] sm:gap-6">
                <dt class="min-w-0">
                  {label}
                  <span class="mt-0.5 block font-mono text-[10px] text-muted-foreground/40">{envVar}</span>
                </dt>
                <dd class="min-w-0 break-all font-mono text-xs">{value}</dd>
              </div>
            {/each}
          </dl>
        </section>
      {/if}

      {#if section === "storage"}
        <section>
          <h3 class="text-sm font-semibold">Storage</h3>
          <dl class="mt-3 grid grid-cols-2 gap-px overflow-hidden rounded-md border border-border bg-border sm:grid-cols-4">
            {#each [["Blobs", String(settings.usage.blobs)], ["Stored", bytes(settings.usage.stored_bytes)], ["Uncompressed", bytes(settings.usage.uncompressed_bytes)], ["Events", String(settings.usage.events)]] as [label, value] (label)}
              <div class="bg-background p-3">
                <dt class="text-[11px] text-muted-foreground">{label}</dt>
                <dd class="mt-0.5 font-mono text-sm">{value}</dd>
              </div>
            {/each}
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

          <p class="mt-6 text-xs text-muted-foreground/50">
            Silt {settings.release} · build {settings.version}
          </p>
        </section>
      {/if}

      <!-- Sticky rather than at the bottom: a save button you have to scroll to
           find is the same complaint as a settings link you have to scroll to
           find. It only shows on the sections that can be saved. -->
      {#if section !== "appearance" && section !== "environment" && section !== "storage"}
        <div class="sticky bottom-0 -mx-1 flex items-center gap-3 border-t border-border bg-background/95 px-1 py-3 backdrop-blur-sm">
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
                : `${overridden.size} setting${overridden.size === 1 ? "" : "s"} set here.`}
            </span>
          {/if}
        </div>
      {/if}
    {:else if !error && section !== "appearance"}
      <p class="text-sm text-muted-foreground">Loading…</p>
    {/if}
  </div>
</div>
