<script lang="ts">
  import { api, type Timeline, type Project } from "$lib/api/client";
  import { link } from "$lib/router.svelte";
  import DensityStrip from "$lib/components/DensityStrip.svelte";
  import Timestamp from "$lib/components/Timestamp.svelte";
  import Empty from "$lib/components/Empty.svelte";
  import { severityDot } from "$lib/format";

  let { reloadKey, projects: knownProjects }: { reloadKey: number; projects: Project[] } = $props();

  const RANGES = [
    { label: "1h", ms: 3_600_000 },
    { label: "6h", ms: 21_600_000 },
    { label: "24h", ms: 86_400_000 },
    { label: "7d", ms: 604_800_000 },
    { label: "30d", ms: 2_592_000_000 },
  ];

  let timeline = $state<Timeline | null>(null);
  let projects = $state<Project[]>([]);
  let showRoutine = $state(false);
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
        projects = p.length > 0 ? p : knownProjects;
        error = null;
      })
      .catch((err: Error) => {
        if (err.name !== "AbortError") error = err.message;
      })
      .finally(() => (loading = false));

    return () => controller.abort();
  });

  const projectName = $derived((id?: number) => projects.find((p) => p.id === id)?.name ?? "");

  // snapshot.changed restates a change marker that is already rendered from
  // timeline.changes, so every configuration change appeared twice. The event
  // still exists in /api/events; it just has no place in a feed that already
  // shows the change itself.
  const DUPLICATE_TYPES = new Set(["snapshot.changed"]);

  // Container lifecycle chatter is the bulk of the feed and rarely what
  // someone opened the page for. It is one toggle away rather than gone.
  const ROUTINE_TYPES = new Set([
    "container.start",
    "container.create",
    "container.stop",
    "container.destroy",
    "container.restart",
    "image.pull",
  ]);

  type Row =
    | { kind: "change"; ts: number; id: string; item: any }
    | { kind: "event"; ts: number; id: string; item: any }
    | { kind: "group"; ts: number; id: string; projects: any[] };

  const feed = $derived.by<Row[]>(() => {
    if (!timeline) return [];

    const rows: Row[] = [];
    for (const c of timeline.changes ?? []) {
      rows.push({ kind: "change", ts: c.taken_at, id: `c${c.id}`, item: c });
    }
    for (const e of timeline.events ?? []) {
      if (DUPLICATE_TYPES.has(e.type)) continue;
      if (!showRoutine && ROUTINE_TYPES.has(e.type)) continue;
      if (severityFilter && e.severity !== severityFilter) continue;
      rows.push({ kind: "event", ts: e.ts, id: `e${e.id}`, item: e });
    }
    rows.sort((a, b) => b.ts - a.ts);

    // A first boot discovers every project at once, producing a wall of
    // identical rows. Collapse changes that land within the same few seconds
    // into one line naming the projects.
    const GROUP_WINDOW_MS = 5000;
    const out: Row[] = [];
    for (let i = 0; i < rows.length; ) {
      const row = rows[i];
      if (row.kind !== "change") {
        out.push(row);
        i++;
        continue;
      }
      let j = i;
      const burst: any[] = [];
      while (j < rows.length && rows[j].kind === "change" && row.ts - rows[j].ts <= GROUP_WINDOW_MS) {
        burst.push((rows[j] as any).item);
        j++;
      }
      if (burst.length > 2) {
        out.push({ kind: "group", ts: row.ts, id: `g${row.id}`, projects: burst });
      } else {
        for (const item of burst) out.push({ kind: "change", ts: item.taken_at, id: `c${item.id}`, item });
      }
      i = j;
    }
    return out.slice(0, 300);
  });

  const hiddenRoutine = $derived(
    (timeline?.events ?? []).filter((e) => ROUTINE_TYPES.has(e.type)).length,
  );
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

    {#if hiddenRoutine > 0 || showRoutine}
      <label class="flex items-center gap-2 text-xs text-muted-foreground">
        <input type="checkbox" bind:checked={showRoutine} class="accent-emerald-500" />
        Container activity{#if !showRoutine && hiddenRoutine > 0}&nbsp;({hiddenRoutine}){/if}
      </label>
    {/if}
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
      {#each feed as row (row.id)}
        <li class="flex items-baseline gap-3 py-2.5 text-sm">
          {#if row.kind === "group"}
            <span class="size-1.5 shrink-0 rounded-full bg-emerald-400" aria-hidden="true"></span>
            <span class="font-medium">{row.projects.length} projects changed</span>
            <span class="min-w-0 truncate text-xs text-muted-foreground">
              {row.projects.map((p) => projectName(p.project_id)).filter(Boolean).join(", ")}
            </span>
          {:else if row.kind === "change"}
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
