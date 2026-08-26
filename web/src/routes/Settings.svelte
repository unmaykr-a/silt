<script lang="ts">
  import { api, type Settings, type PruneResult } from "$lib/api/client";
  import { bytes, duration } from "$lib/format";
  import { Button } from "$lib/components/ui/button";

  let settings = $state<Settings | null>(null);
  let error = $state<string | null>(null);
  let pruning = $state(false);
  let pruned = $state<PruneResult | null>(null);

  $effect(() => {
    const controller = new AbortController();
    api
      .settings(controller.signal)
      .then((s) => {
        settings = s;
        error = null;
      })
      .catch((err: Error) => {
        if (err.name !== "AbortError") error = err.message;
      });
    return () => controller.abort();
  });

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

  const rows = $derived.by(() => {
    if (!settings) return [] as Array<[string, string, string]>;
    return [
      ["Host name", settings.host_name, "SILT_HOST_NAME"],
      ["Docker endpoint", settings.docker_host, "SILT_DOCKER_HOST"],
      ["Database", settings.db_path, "SILT_DB_PATH"],
      ["Snapshot interval", duration(settings.snapshot_interval_ms), "SILT_SNAPSHOT_INTERVAL"],
      ["Changed snapshots kept", `${settings.retention_days} days`, "SILT_RETENTION_DAYS"],
      ["Runtime-only snapshots kept", `${settings.unchanged_retention_days} days`, "SILT_UNCHANGED_RETENTION_DAYS"],
      ["Events kept", `${settings.event_retention_days} days`, "SILT_EVENT_RETENTION_DAYS"],
      ["Retention pass", duration(settings.retention_interval_ms), "SILT_RETENTION_INTERVAL"],
      ["Vacuum", settings.vacuum_interval_ms > 0 ? duration(settings.vacuum_interval_ms) : "disabled", "SILT_VACUUM_INTERVAL"],
      ["Ingest webhook", settings.ingest_configured ? "configured" : "not configured", "SILT_INGEST_TOKEN"],
      ["Log level", settings.log_level, "SILT_LOG_LEVEL"],
    ];
  });
</script>

<div class="space-y-8">
  <header>
    <h2 class="text-2xl font-semibold tracking-tight">Settings</h2>
    <p class="mt-1 text-sm text-muted-foreground">
      Silt is configured by environment variables, so this shows what is in force rather than
      offering to change it. Edit your compose file and recreate the container to change anything
      here.
    </p>
  </header>

  {#if error}
    <p class="rounded border border-red-900/60 bg-red-950/40 px-4 py-3 text-sm text-red-300">{error}</p>
  {/if}

  {#if settings}
    <section>
      <h3 class="text-xs font-medium uppercase tracking-wide text-muted-foreground">Configuration</h3>
      <dl class="mt-3 divide-y divide-border border-y border-border">
        {#each rows as [label, value, envVar] (envVar)}
          <div class="flex items-baseline gap-3 py-2 text-sm">
            <dt class="w-56 shrink-0">{label}</dt>
            <!-- min-w-0 lets a long value (a database path) wrap inside its own
                 column instead of pushing the variable name onto a new line. -->
            <dd class="min-w-0 flex-1 break-all font-mono text-xs">{value}</dd>
            <dd class="shrink-0 font-mono text-xs text-muted-foreground/50">{envVar}</dd>
          </div>
        {/each}
      </dl>
    </section>

    <section>
      <h3 class="text-xs font-medium uppercase tracking-wide text-muted-foreground">
        Environment keys kept readable
      </h3>
      <p class="mt-1 text-xs text-muted-foreground/70">
        Every environment value is redacted unless its key is on the built-in safe list
        (<span class="font-mono">PUID</span>, <span class="font-mono">TZ</span>,
        <span class="font-mono">LOG_LEVEL</span>, …). These are the extras you added.
      </p>
      {#if settings.keep_keys.length === 0}
        <p class="mt-2 font-mono text-xs text-muted-foreground">
          none — set SILT_KEEP_KEYS to add more
        </p>
      {:else}
        <p class="mt-2 font-mono text-xs">{settings.keep_keys.join(", ")}</p>
      {/if}
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

    <p class="text-xs text-muted-foreground/50">Silt {settings.version}</p>
  {/if}
</div>
