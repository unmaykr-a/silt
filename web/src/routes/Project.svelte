<script lang="ts">
  import { api, type Project, type Snapshot, type SnapshotDetail } from "$lib/api/client";
  import { link, router } from "$lib/router.svelte";
  import Timestamp from "$lib/components/Timestamp.svelte";
  import Empty from "$lib/components/Empty.svelte";
  import { shortDigest, duration } from "$lib/format";
  import { Button } from "$lib/components/ui/button";

  let { projectId, reloadKey }: { projectId: number; reloadKey: number } = $props();

  let project = $state<Project | null>(null);
  let snapshots = $state<Snapshot[]>([]);
  let latest = $state<SnapshotDetail | null>(null);
  let error = $state<string | null>(null);
  let changedOnly = $state(false);
  let snapshotting = $state(false);

  $effect(() => {
    const key = [projectId, changedOnly, reloadKey];
    void key;

    const controller = new AbortController();
    (async () => {
      try {
        const [p, snaps] = await Promise.all([
          api.project(projectId, controller.signal),
          api.snapshots(projectId, { changedOnly, limit: 100 }, controller.signal),
        ]);
        project = p;
        snapshots = snaps;
        latest = snaps.length > 0 ? await api.snapshot(snaps[0].id, controller.signal) : null;
        error = null;
      } catch (err) {
        if ((err as Error).name !== "AbortError") error = (err as Error).message;
      }
    })();
    return () => controller.abort();
  });

  // The two most recent snapshots where the configuration actually changed.
  // Comparing the last two observations would usually diff a thing against
  // itself, since unchanged observations do not create rows.
  const lastTwoChanges = $derived(snapshots.filter((s) => s.config_changed).slice(0, 2));

  function compareLastTwo() {
    const [newer, older] = lastTwoChanges;
    if (!newer || !older) return;
    router.navigate(`/diff?from=${older.id}&to=${newer.id}&project=${projectId}`);
  }

  async function takeSnapshot() {
    snapshotting = true;
    try {
      await api.takeSnapshot(projectId);
      snapshots = await api.snapshots(projectId, { changedOnly, limit: 100 });
      error = null;
    } catch (err) {
      error = (err as Error).message;
    } finally {
      snapshotting = false;
    }
  }
</script>

{#if error}
  <p class="rounded border border-red-900/60 bg-red-950/40 px-4 py-3 text-sm text-red-300">{error}</p>
{/if}

{#if project}
  <div class="space-y-8">
    <header class="flex flex-wrap items-baseline justify-between gap-3">
      <div>
        <h2 class="text-2xl font-semibold tracking-tight">{project.name}</h2>
        {#if project.working_dir}
          <p class="mt-0.5 font-mono text-xs text-muted-foreground">{project.working_dir}</p>
        {/if}
      </div>
      <div class="flex gap-2">
        <Button variant="outline" size="sm" onclick={takeSnapshot} disabled={snapshotting}>
          {snapshotting ? "Snapshotting…" : "Snapshot now"}
        </Button>
        <Button
          variant="secondary"
          size="sm"
          onclick={compareLastTwo}
          disabled={lastTwoChanges.length < 2}
          title={lastTwoChanges.length < 2 ? "Needs two configuration changes to compare" : ""}
        >
          Compare last two changes
        </Button>
      </div>
    </header>

    <section>
      <h3 class="text-xs font-medium uppercase tracking-wide text-muted-foreground">Services</h3>
      {#if !latest || latest.services.length === 0}
        <div class="mt-3"><Empty title="No services recorded yet." /></div>
      {:else}
        <div class="mt-3 overflow-x-auto">
          <table class="w-full min-w-[42rem] text-sm">
            <thead>
              <tr class="border-b border-border text-left text-xs text-muted-foreground">
                <th class="pb-2 font-medium">Service</th>
                <th class="pb-2 font-medium">Image</th>
                <th class="pb-2 font-medium">Digest</th>
                <th class="pb-2 font-medium">State</th>
                <th class="pb-2 font-medium">Health</th>
                <th class="pb-2 text-right font-medium">Restarts</th>
                <th class="pb-2 text-right font-medium">Uptime</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-border">
              {#each latest.services as svc (svc.service)}
                <tr>
                  <td class="py-2">
                    <a
                      use:link
                      href="/projects/{projectId}/services/{encodeURIComponent(svc.service)}"
                      class="font-medium underline-offset-4 hover:underline"
                    >
                      {svc.service}
                    </a>
                  </td>
                  <td class="py-2 font-mono text-xs text-muted-foreground">{svc.image_ref ?? ""}</td>
                  <td class="py-2 font-mono text-xs text-muted-foreground" title={svc.image_digest ?? svc.image_id ?? ""}>
                    {shortDigest(svc.image_digest || svc.image_id)}
                  </td>
                  <td class="py-2 text-xs">{svc.state ?? ""}</td>
                  <td class="py-2 text-xs {svc.health === 'unhealthy' ? 'text-red-400' : ''}">
                    {svc.health || "—"}
                  </td>
                  <td class="py-2 text-right text-xs {svc.restart_count > 0 ? 'text-amber-400' : 'text-muted-foreground'}">
                    {svc.restart_count}
                  </td>
                  <td class="py-2 text-right text-xs text-muted-foreground">
                    {svc.started_at ? duration(Date.now() - svc.started_at) : "—"}
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    </section>

    <section>
      <div class="flex items-baseline justify-between">
        <h3 class="text-xs font-medium uppercase tracking-wide text-muted-foreground">
          Snapshots ({snapshots.length})
        </h3>
        <label class="flex items-center gap-2 text-xs text-muted-foreground">
          <input type="checkbox" bind:checked={changedOnly} class="accent-emerald-500" />
          Configuration changes only
        </label>
      </div>

      {#if snapshots.length === 0}
        <div class="mt-3"><Empty title="No snapshots yet." /></div>
      {:else}
        <ul class="mt-3 divide-y divide-border border-y border-border">
          {#each snapshots as snap, i (snap.id)}
            <li class="flex items-baseline gap-3 py-2 text-sm">
              <span
                class="size-1.5 shrink-0 rounded-full {snap.config_changed
                  ? 'bg-emerald-400'
                  : snap.runtime_changed
                    ? 'bg-amber-500'
                    : 'bg-zinc-700'}"
                aria-hidden="true"
              ></span>
              <span class="w-32 shrink-0 text-xs">
                {#if snap.config_changed}
                  config changed
                {:else if snap.runtime_changed}
                  runtime changed
                {:else}
                  observed
                {/if}
              </span>
              <span class="text-xs text-muted-foreground">via {snap.trigger}</span>
              {#if snap.observation_count > 1}
                <span class="text-xs text-muted-foreground/60" title="Identical observations update this snapshot instead of adding rows">
                  ×{snap.observation_count}
                </span>
              {/if}
              {#if snapshots[i + 1]}
                <a
                  use:link
                  href="/diff?from={snapshots[i + 1].id}&to={snap.id}&project={projectId}"
                  class="text-xs text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
                >
                  diff
                </a>
              {/if}
              <Timestamp ts={snap.taken_at} class="ml-auto shrink-0 text-xs text-muted-foreground" />
            </li>
          {/each}
        </ul>
      {/if}
    </section>
  </div>
{/if}
