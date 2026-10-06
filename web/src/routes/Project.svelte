<script lang="ts">
  import {
    api,
    type Project,
    type Snapshot,
    type SnapshotDetail,
    type Timeline,
  } from "$lib/api/client";
  import { link, router } from "$lib/router.svelte";
  import Timestamp from "$lib/components/Timestamp.svelte";
  import Empty from "$lib/components/Empty.svelte";
  import { shortDigest, duration } from "$lib/format";
  import { serviceState } from "$lib/servicestate";
  import { Button } from "$lib/components/ui/button";
  import DensityStrip from "$lib/components/DensityStrip.svelte";
  import Segmented from "$lib/components/Segmented.svelte";
  import EventDetail from "$lib/components/EventDetail.svelte";
  import { clockTime, datetime, relative, severityAccent } from "$lib/format";

  let { projectId, reloadKey }: { projectId: number; reloadKey: number } = $props();

  // This project's own activity, on the same strip the timeline uses. The
  // fleet timeline answers "what happened on this host"; standing on a project
  // page and having to go back and filter to answer it for the one stack in
  // front of you was the gap.
  let projectTimeline = $state<Timeline | null>(null);
  let rangeLabel = $state("604800000");
  const rangeMs = $derived(Number(rangeLabel));

  // Pinned when the strip is fetched rather than read per render, so the
  // selected window does not drift under the selection while it is open.
  let nowish = $state(Date.now());

  // A window dragged out on the strip. The strip has always supported the drag
  // — the fleet timeline uses it — but this page passed no handler, so the
  // gesture did nothing here and there was nowhere for it to put the answer.
  let zoom = $state<{ from: number; to: number } | null>(null);
  let openEvent = $state<number | null>(null);

  // What happened in the selected window, this project only. Standing on a
  // project, seeing a spike, and having to go to the fleet timeline and filter
  // back down to the stack already in front of you was the gap.
  const windowFrom = $derived(zoom ? zoom.from : nowish - rangeMs);
  const windowTo = $derived(zoom ? zoom.to : nowish);
  const windowEvents = $derived(
    (projectTimeline?.events ?? []).filter((e) => e.ts >= windowFrom && e.ts <= windowTo),
  );

  const RANGES = [
    { value: "86400000", label: "24h" },
    { value: "604800000", label: "7d" },
    { value: "2592000000", label: "30d" },
    { value: "7776000000", label: "90d" },
  ];

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

  // Separate from the main fetch: changing the range should redraw the strip
  // without refetching the snapshot list and its detail behind it.
  $effect(() => {
    const key = [projectId, rangeMs, reloadKey];
    void key;
    const controller = new AbortController();
    const to = Date.now();
    nowish = to;
    api
      .timeline({ project: projectId, from: to - rangeMs, to }, controller.signal)
      .then((t) => (projectTimeline = t))
      .catch(() => {});
    return () => controller.abort();
  });

  // Changing the range is a new question, so a selection made against the old
  // one is not an answer to it.
  $effect(() => {
    void rangeMs;
    zoom = null;
  });

  const exportHref = $derived(
    `/api/events/export?project=${projectId}&from=${Math.round(windowFrom)}&to=${Math.round(windowTo)}`,
  );

  function windowLabel(from: number, to: number): string {
    const sameDay = new Date(from).toDateString() === new Date(to).toDateString();
    return sameDay
      ? `${datetime(from)} to ${clockTime(to)}`
      : `${datetime(from)} to ${datetime(to)}`;
  }

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
        <a
          use:link
          href="/projects/{projectId}/files"
          class="inline-flex h-8 items-center rounded-md border border-border px-3 text-xs text-muted-foreground transition-colors hover:text-foreground"
        >
          Compose files
        </a>
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
      <div class="flex flex-wrap items-center justify-between gap-2">
        <h3 class="text-xs font-medium uppercase tracking-wide text-muted-foreground">Activity</h3>
        <Segmented label="Range" size="xs" bind:value={rangeLabel} options={RANGES} />
      </div>
      <div class="mt-3">
        <DensityStrip
          timeline={projectTimeline}
          zoomed={zoom !== null}
          onZoom={(from, to) => (zoom = { from, to })}
          onReset={() => (zoom = null)}
        />
      </div>

      <div class="mt-3 flex flex-wrap items-center gap-2">
        {#if zoom}
          <button
            type="button"
            class="inline-flex items-center gap-1.5 rounded-md border border-border px-2 py-1 text-xs
                   text-muted-foreground transition-colors hover:bg-secondary/60 hover:text-foreground"
            onclick={() => (zoom = null)}
          >
            <span class="font-mono">{windowLabel(zoom.from, zoom.to)}</span>
            <span aria-hidden="true">×</span>
            <span class="sr-only">Clear the selected window</span>
          </button>
        {:else}
          <span class="text-xs text-muted-foreground/60">Drag across the chart to select a window.</span>
        {/if}

        {#if windowEvents.length > 0}
          <a
            href={exportHref}
            download
            class="ml-auto inline-flex items-center gap-1.5 rounded-md border border-border px-2 py-1
                   text-xs text-muted-foreground no-underline transition-colors hover:bg-secondary/60 hover:text-foreground"
            title="This project's events in this window, as a file"
          >
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
              <path d="M12 3v12m0 0 4-4m-4 4-4-4M4 17v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2" />
            </svg>
            Export
          </a>
        {/if}
      </div>

      <!-- The events themselves, which this page never showed. A strip with a
           spike on it and no way to read the spike is a chart of something
           rather than an answer about it. -->
      {#if windowEvents.length === 0}
        <p class="mt-3 text-xs text-muted-foreground/70">
          {zoom ? "Nothing happened in the selected window." : "No events recorded in this range."}
        </p>
      {:else}
        <div class="mt-3">
          {#each windowEvents as e (e.id)}
            <button
              type="button"
              class="flex w-full items-baseline gap-3 border-b border-border/60 py-2 text-left text-sm
                     transition-colors hover:bg-secondary/30"
              onclick={() => (openEvent = e.id)}
            >
              <span class="w-1 shrink-0 self-stretch rounded-sm {severityAccent(e.severity)}"></span>
              <span
                class="w-16 shrink-0 font-mono text-xs tabular-nums text-muted-foreground"
                title={datetime(e.ts, { seconds: true })}
              >
                {clockTime(e.ts)}
              </span>
              <span class="shrink-0 font-mono text-xs">{e.type}</span>
              {#if e.service}
                <span class="shrink-0 text-muted-foreground">{e.service}</span>
              {/if}
              {#if e.message}
                <span class="min-w-0 flex-1 truncate text-xs text-muted-foreground/70" title={e.message}>
                  {e.message}
                </span>
              {/if}
              <span class="ml-auto shrink-0 text-xs text-muted-foreground/50">{relative(e.ts)}</span>
            </button>
          {/each}
        </div>
      {/if}
    </section>

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
                <th class="pb-2 text-right font-medium">Restarts</th>
                <th class="pb-2 text-right font-medium">Uptime</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-border">
              {#each latest.services as svc (svc.service)}
                {@const st = serviceState(svc)}
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
                  <!-- State and health were two columns of raw Docker
                       strings, so "running / unhealthy" and "exited / —" were
                       for the reader to interpret. One column, one verdict,
                       with the reason on hover. -->
                  <td class="py-2 text-xs">
                    <span class="inline-flex items-center gap-1.5" title={st.detail}>
                      <span class="size-2 shrink-0 rounded-full {st.dot}"></span>
                      <span class={st.text}>{st.label}</span>
                    </span>
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
              <!-- A file change with no config change is drift: an edit
                   nobody applied. It was recorded from the first release and
                   never reported here, so the row read "observed" — a snapshot
                   where, as far as this list was concerned, nothing had
                   happened. -->
              <span
                class="size-1.5 shrink-0 rounded-full {snap.config_changed
                  ? 'bg-emerald-400'
                  : snap.files_changed
                    ? 'bg-sky-400'
                    : snap.runtime_changed
                      ? 'bg-amber-500'
                      : 'bg-zinc-700'}"
                aria-hidden="true"
              ></span>
              <span class="w-32 shrink-0 text-xs">
                {#if snap.config_changed}
                  config changed
                {:else if snap.files_changed}
                  <span class="text-sky-600 dark:text-sky-400" title="A compose file on disk changed and the running stack did not">
                    file edited
                  </span>
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

<EventDetail bind:eventId={openEvent} />
