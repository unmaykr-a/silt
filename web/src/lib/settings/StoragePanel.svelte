<script lang="ts">
  import { Button } from "$lib/components/ui/button";
  import { bytes } from "$lib/format";
  import { api, type PruneResult } from "$lib/api/client";
  import type { SettingsStore } from "./store.svelte";

  let { store }: { store: SettingsStore } = $props();
  const settings = $derived(store.settings!);

  let pruning = $state(false);
  let pruned = $state<PruneResult | null>(null);
  let importError = $state<string | null>(null);

  /** Both downloads are a plain navigation: the server sets the filename, and
      holding a file in memory to hand it to the browser that already knows how
      to save a download would be work for nothing. */
  const download = (path: string) => () => (window.location.href = path);

  /** Restore. The file's `settings` object is exactly what PUT takes, so this
      is the ordinary write and gets the ordinary validation. */
  async function importSettings(event: Event) {
    const input = event.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    input.value = "";
    if (!file) return;

    importError = null;
    try {
      const doc = JSON.parse(await file.text());
      const patch = doc?.settings ?? doc;
      if (!patch || typeof patch !== "object" || Array.isArray(patch)) {
        throw new Error("that file has no settings in it");
      }
      store.adopt(await api.updateSettings(patch));
      store.notice = doc?.omitted?.length
        ? `Restored. Set again by hand: ${doc.omitted.join(", ")}.`
        : "Restored.";
      store.error = null;
    } catch (err) {
      importError = (err as Error).message;
    }
  }

  async function prune() {
    pruning = true;
    pruned = await store.prune();
    pruning = false;
  }
</script>

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

  <!-- Above the settings export on purpose: settings can be retyped, and the
       history is the thing that cannot be reconstructed. -->
  <div class="mt-6 border-t border-border pt-5">
    <h4 class="text-sm font-medium">Back up the history</h4>
    <p class="mt-1 max-w-2xl text-xs leading-relaxed text-muted-foreground">
      A SQLite file written as one consistent snapshot, safe to take while Silt is running. Copying
      <code class="font-mono text-[11px]">silt.db</code> off the volume is not: the database runs in
      WAL mode, so a copy of that one file opens cleanly and is quietly missing whatever had not been
      checkpointed. Restore it by putting this where
      <code class="font-mono text-[11px]">SILT_DB_PATH</code> points.
    </p>
    <div class="mt-3 flex flex-wrap items-center gap-3">
      <Button variant="outline" size="sm" onclick={download("/api/maintenance/backup")} disabled={store.readOnly}>
        Download backup
      </Button>
      <span class="text-xs text-muted-foreground">
        about {bytes(settings.usage.stored_bytes)} — or point your own backup at
        <code class="font-mono text-[11px]">/api/maintenance/backup</code>
      </span>
    </div>
  </div>

  <!-- Settings are already a sparse patch on top of the environment, so the
       export is that document with a header. There is no import endpoint: PUT
       /api/settings already takes this shape, and a second write path would be
       a second set of validation rules to keep in step. -->
  <div class="mt-6 border-t border-border pt-5">
    <h4 class="text-sm font-medium">Move this configuration</h4>
    <p class="mt-1 max-w-2xl text-xs leading-relaxed text-muted-foreground">
      A file of everything set here rather than by the environment. Secrets are left out and named in
      the file — a notification target restored as a blank is a restore that quietly stops notifying,
      so it says which ones you will have to set again.
    </p>
    <div class="mt-3 flex flex-wrap items-center gap-3">
      <Button variant="outline" size="sm" onclick={download("/api/settings/export")}>Download settings</Button>
      {#if !store.readOnly}
        <label class="cursor-pointer text-xs text-muted-foreground underline underline-offset-2 hover:text-foreground">
          <input type="file" accept="application/json,.json" class="sr-only" onchange={importSettings} />
          Restore from a file
        </label>
      {/if}
      {#if importError}
        <span class="text-xs text-red-500 dark:text-red-300">{importError}</span>
      {/if}
    </div>
  </div>

  <div class="mt-6 border-t border-border pt-5">
    <Button variant="outline" size="sm" onclick={prune} disabled={pruning || store.readOnly}>
      {pruning ? "Pruning…" : "Run retention pass now"}
    </Button>
  </div>

  {#if pruned}
    <p class="mt-2 text-xs text-muted-foreground">
      Removed {pruned.unchanged_snapshots} runtime-only and {pruned.changed_snapshots} changed snapshots,
      {pruned.events} events, {pruned.blobs} blobs.
    </p>
  {/if}

  <p class="mt-6 text-xs text-muted-foreground/50">
    Silt {settings.release} · build {settings.version}
  </p>
</section>
