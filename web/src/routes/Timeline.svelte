<script lang="ts">
  import { api, type Timeline, type Project } from "$lib/api/client";
  import { link } from "$lib/router.svelte";
  import DensityStrip from "$lib/components/DensityStrip.svelte";
  import Empty from "$lib/components/Empty.svelte";
  import { clockTime, datetime, dateOnly, relative } from "$lib/format";

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
  // A window dragged out on the density strip. It supersedes rangeMs until it
  // is cleared, which is what makes "drag across the spike, read the feed"
  // work: the strip and the list below it share one window.
  let zoom = $state<{ from: number; to: number } | null>(null);
  let projectFilter = $state(0);
  let severityFilter = $state("");
  let error = $state<string | null>(null);
  let loading = $state(true);
  // Bursts collapse into one row by default. Expanding one names the projects
  // rather than sending you to another screen to find out.
  let expanded = $state<Record<string, boolean>>({});

  let now = $state(Date.now());
  $effect(() => {
    const id = setInterval(() => (now = Date.now()), 30_000);
    return () => clearInterval(id);
  });

  $effect(() => {
    // Re-runs whenever a filter changes or an SSE event bumps reloadKey.
    const key = [rangeMs, projectFilter, reloadKey, zoom];
    void key;

    const controller = new AbortController();
    const to = zoom ? zoom.to : Date.now();
    const from = zoom ? zoom.from : to - rangeMs;

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

  // Day headings, so a 30-day window does not read as one undifferentiated
  // column of times.
  type Entry = { kind: "day"; key: string; label: string } | { kind: "row"; key: string; row: Row };

  const entries = $derived.by<Entry[]>(() => {
    const out: Entry[] = [];
    let day = "";
    for (const row of feed) {
      const stamp = new Date(row.ts).toDateString();
      if (stamp !== day) {
        day = stamp;
        out.push({ kind: "day", key: `d${stamp}`, label: dayLabel(row.ts) });
      }
      out.push({ kind: "row", key: row.id, row });
    }
    return out;
  });

  function dayLabel(ts: number): string {
    const today = new Date().toDateString();
    const yesterday = new Date(Date.now() - 86_400_000).toDateString();
    const stamp = new Date(ts).toDateString();
    if (stamp === today) return "Today";
    if (stamp === yesterday) return "Yesterday";
    return dateOnly(ts);
  }

  const hiddenRoutine = $derived(
    (timeline?.events ?? []).filter((e) => ROUTINE_TYPES.has(e.type)).length,
  );

  // The zoomed window, rendered short: same day means one date, otherwise two.
  function windowLabel(from: number, to: number): string {
    const sameDay = new Date(from).toDateString() === new Date(to).toDateString();
    return sameDay ? `${datetime(from)} → ${clockTime(to)}` : `${datetime(from)} → ${datetime(to)}`;
  }

  // A severity's accent, used as a 2px bar rather than a dot: at a glance you
  // read the colour of the column, not three separate circles.
  function accent(severity: string): string {
    switch (severity) {
      case "high":
      case "error":
        return "bg-red-500";
      case "medium":
      case "warn":
        return "bg-amber-500";
      default:
        return "bg-zinc-400/50 dark:bg-zinc-600";
    }
  }

  const control =
    "rounded-md border border-border bg-background px-2.5 py-1.5 text-xs text-foreground outline-none focus:ring-2 focus:ring-ring";
</script>

<div class="space-y-4">
  <div class="flex flex-wrap items-center gap-2">
    <div class="flex rounded-md border border-border">
      {#each RANGES as range (range.label)}
        <button
          class="px-2.5 py-1.5 text-xs transition-colors first:rounded-l-md last:rounded-r-md
                 {!zoom && rangeMs === range.ms
            ? 'bg-secondary text-secondary-foreground'
            : 'text-muted-foreground hover:text-foreground'}"
          onclick={() => {
            zoom = null;
            rangeMs = range.ms;
          }}
        >
          {range.label}
        </button>
      {/each}
    </div>

    {#if zoom}
      <button
        class="inline-flex items-center gap-2 rounded-md border border-border px-2.5 py-1.5 text-xs
               transition-colors hover:bg-secondary/60"
        onclick={() => (zoom = null)}
        title="Back to the selected range"
      >
        <span class="font-mono">{windowLabel(zoom.from, zoom.to)}</span>
        <span class="text-muted-foreground">×</span>
      </button>
    {/if}

    <select bind:value={projectFilter} class={control} aria-label="Project">
      <option value={0}>All projects</option>
      {#each projects as project (project.id)}
        <option value={project.id}>{project.name}</option>
      {/each}
    </select>

    <select bind:value={severityFilter} class={control} aria-label="Severity">
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
    <p class="rounded-md border border-red-500/40 bg-red-500/10 px-4 py-2.5 text-sm text-red-500 dark:text-red-300">
      {error}
    </p>
  {/if}

  <DensityStrip
    {timeline}
    zoomed={zoom !== null}
    onZoom={(from, to) => (zoom = { from, to })}
    onReset={() => (zoom = null)}
  />

  {#if loading && feed.length === 0}
    <p class="text-sm text-muted-foreground">Loading…</p>
  {:else if feed.length === 0}
    <Empty
      title="Nothing recorded in this window."
      hint="Silt records a change when a project's configuration differs from the last observation."
    />
  {:else}
    <!-- One hairline between rows instead of a box around each: the previous
         version drew a border for every row and then squeezed the content
         inside it, which read as heavy and cramped at the same time. -->
    <div>
      {#each entries as entry (entry.key)}
        {#if entry.kind === "day"}
          <div
            class="sticky top-0 z-10 -mx-1 mb-px mt-4 bg-background/90 px-1 py-1.5 text-[11px] font-medium
                   uppercase tracking-wide text-muted-foreground/60 backdrop-blur-sm first:mt-0"
          >
            {entry.label}
          </div>
        {:else if entry.row.kind === "group"}
          {@const row = entry.row}
          <div class="border-b border-border/60">
            <button
              type="button"
              class="flex w-full items-baseline gap-3 py-2 text-left text-sm transition-colors hover:bg-secondary/30"
              onclick={() => (expanded = { ...expanded, [row.id]: !expanded[row.id] })}
            >
              <span class="w-1 shrink-0 self-stretch rounded-sm bg-emerald-500"></span>
              <span class="w-14 shrink-0 font-mono text-xs tabular-nums text-muted-foreground">
                {clockTime(row.ts)}
              </span>
              <span class="font-medium">{row.projects.length} projects changed</span>
              <span class="min-w-0 flex-1 truncate text-xs text-muted-foreground">
                {row.projects.map((p) => projectName(p.project_id)).filter(Boolean).join(", ")}
              </span>
              <span class="shrink-0 text-xs text-muted-foreground/50">
                {expanded[row.id] ? "hide" : "show"}
              </span>
            </button>
            {#if expanded[row.id]}
              <ul class="mb-2 ml-[4.75rem] grid gap-x-6 gap-y-0.5 sm:grid-cols-2 lg:grid-cols-3">
                {#each row.projects as p (p.id)}
                  <li class="truncate text-xs">
                    <a
                      use:link
                      href="/diff?to={p.id}&project={p.project_id}"
                      class="text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
                    >
                      {projectName(p.project_id) || `project ${p.project_id}`}
                    </a>
                  </li>
                {/each}
              </ul>
            {/if}
          </div>
        {:else if entry.row.kind === "change"}
          {@const row = entry.row}
          <div class="flex items-baseline gap-3 border-b border-border/60 py-2 text-sm">
            <span class="w-1 shrink-0 self-stretch rounded-sm bg-emerald-500"></span>
            <span
              class="w-14 shrink-0 font-mono text-xs tabular-nums text-muted-foreground"
              title={datetime(row.ts, { seconds: true })}
            >
              {clockTime(row.ts)}
            </span>
            <a
              use:link
              href="/diff?to={row.item.id}&project={row.item.project_id}"
              class="shrink-0 font-medium underline-offset-4 hover:underline"
            >
              configuration changed
            </a>
            <a
              use:link
              href="/projects/{row.item.project_id}"
              class="min-w-0 truncate text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
            >
              {projectName(row.item.project_id)}
            </a>
            <span class="ml-auto shrink-0 text-xs text-muted-foreground/50">
              via {row.item.trigger} · {relative(row.ts, now)}
            </span>
          </div>
        {:else}
          {@const row = entry.row}
          <div class="flex items-baseline gap-3 border-b border-border/60 py-2 text-sm">
            <span class="w-1 shrink-0 self-stretch rounded-sm {accent(row.item.severity)}"></span>
            <span
              class="w-14 shrink-0 font-mono text-xs tabular-nums text-muted-foreground"
              title={datetime(row.ts, { seconds: true })}
            >
              {clockTime(row.ts)}
            </span>
            <span class="shrink-0 font-mono text-xs">{row.item.type}</span>
            {#if row.item.service}
              <span class="shrink-0 text-muted-foreground">{row.item.service}</span>
            {/if}
            {#if row.item.message}
              <span class="min-w-0 flex-1 truncate text-xs text-muted-foreground/70" title={row.item.message}>
                {row.item.message}
              </span>
            {/if}
            <span class="ml-auto shrink-0 text-xs text-muted-foreground/50">{relative(row.ts, now)}</span>
          </div>
        {/if}
      {/each}
    </div>
  {/if}
</div>
