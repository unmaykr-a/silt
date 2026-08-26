<script lang="ts">
  import { api, type Timeline, type Project } from "$lib/api/client";
  import { link } from "$lib/router.svelte";
  import DensityStrip from "$lib/components/DensityStrip.svelte";
  import Timestamp from "$lib/components/Timestamp.svelte";
  import Empty from "$lib/components/Empty.svelte";
  import { severityDot } from "$lib/format";

  let { reloadKey }: { reloadKey: number } = $props();

  const RANGES = [
    { label: "1h", ms: 3_600_000 },
    { label: "6h", ms: 21_600_000 },
    { label: "24h", ms: 86_400_000 },
    { label: "7d", ms: 604_800_000 },
    { label: "30d", ms: 2_592_000_000 },
  ];

  let timeline = $state<Timeline | null>(null);
  let projects = $state<Project[]>([]);
  let rangeMs = $state(86_400_000);
  let projectFilter = $state(0);
  let severityFilter = $state("");
  let error = $state<string | null>(null);
  let loading = $state(true);

  $effect(() => {
    // Re-runs whenever a filter changes or an SSE event bumps reloadKey.
    const key = [rangeMs, projectFilter, reloadKey];
    void key;

    const controller = new AbortController();
    const to = Date.now();
    const from = to - rangeMs;

    Promise.all([
      api.timeline({ from, to, project: projectFilter || undefined }, controller.signal),
      api.projects(controller.signal),
    ])
      .then(([t, p]) => {
        timeline = t;
        projects = p;
        error = null;
      })
      .catch((err: Error) => {
        if (err.name !== "AbortError") error = err.message;
      })
      .finally(() => (loading = false));

    return () => controller.abort();
  });

  const projectName = $derived((id?: number) => projects.find((p) => p.id === id)?.name ?? "");

  const feed = $derived.by(() => {
    if (!timeline) return [] as Array<{ kind: "change" | "event"; ts: number; item: unknown }>;
    const rows: Array<{ kind: "change" | "event"; ts: number; item: any }> = [];
    for (const c of timeline.changes ?? []) rows.push({ kind: "change", ts: c.taken_at, item: c });
    for (const e of timeline.events ?? []) {
      if (severityFilter && e.severity !== severityFilter) continue;
      rows.push({ kind: "event", ts: e.ts, item: e });
    }
    rows.sort((a, b) => b.ts - a.ts);
    return rows.slice(0, 200);
  });
</script>

<div class="space-y-6">
  <div class="flex flex-wrap items-center gap-2">
    <div class="flex rounded-md border border-border">
      {#each RANGES as range (range.label)}
        <button
          class="px-3 py-1.5 text-xs transition-colors first:rounded-l-md last:rounded-r-md
                 {rangeMs === range.ms ? 'bg-secondary text-secondary-foreground' : 'text-muted-foreground hover:text-foreground'}"
          onclick={() => (rangeMs = range.ms)}
        >
          {range.label}
        </button>
      {/each}
    </div>

    <select
      bind:value={projectFilter}
      class="rounded-md border border-border bg-background px-3 py-1.5 text-xs text-foreground"
    >
      <option value={0}>All projects</option>
      {#each projects as project (project.id)}
        <option value={project.id}>{project.name}</option>
      {/each}
    </select>

    <select
      bind:value={severityFilter}
      class="rounded-md border border-border bg-background px-3 py-1.5 text-xs text-foreground"
    >
      <option value="">All severities</option>
      <option value="error">Errors</option>
      <option value="warn">Warnings</option>
      <option value="info">Info</option>
    </select>
  </div>

  {#if error}
    <p class="rounded border border-red-900/60 bg-red-950/40 px-4 py-3 text-sm text-red-300">{error}</p>
  {/if}

  <DensityStrip {timeline} />

  {#if loading && feed.length === 0}
    <p class="text-sm text-muted-foreground">Loading…</p>
  {:else if feed.length === 0}
    <Empty
      title="Nothing recorded in this window."
      hint="Silt records a change when a project's configuration differs from the last observation."
    />
  {:else}
    <ul class="divide-y divide-border border-y border-border">
      {#each feed as row (row.kind + row.item.id)}
        <li class="flex items-baseline gap-3 py-2.5 text-sm">
          {#if row.kind === "change"}
            <span class="size-1.5 shrink-0 rounded-full bg-emerald-400" aria-hidden="true"></span>
            <a
              use:link
              href="/diff?to={row.item.id}&project={row.item.project_id}"
              class="font-medium underline-offset-4 hover:underline"
            >
              configuration changed
            </a>
            <a
              use:link
              href="/projects/{row.item.project_id}"
              class="text-muted-foreground underline-offset-4 hover:underline"
            >
              {projectName(row.item.project_id)}
            </a>
            <span class="text-xs text-muted-foreground/70">via {row.item.trigger}</span>
          {:else}
            <span
              class="size-1.5 shrink-0 rounded-full {severityDot(row.item.severity)}"
              aria-hidden="true"
            ></span>
            <span class="font-mono text-xs">{row.item.type}</span>
            {#if row.item.service}
              <span class="text-muted-foreground">{row.item.service}</span>
            {/if}
            {#if row.item.message}
              <span class="truncate text-xs text-muted-foreground/70">{row.item.message}</span>
            {/if}
          {/if}
          <Timestamp ts={row.ts} class="ml-auto shrink-0 text-xs text-muted-foreground" />
        </li>
      {/each}
    </ul>
  {/if}
</div>
