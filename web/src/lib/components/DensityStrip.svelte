<script lang="ts">
  import uPlot from "uplot";
  import "uplot/dist/uPlot.min.css";
  import type { Timeline } from "$lib/api/client";
  import { theme } from "$lib/theme.svelte";
  import { prefs } from "$lib/prefs.svelte";
  import { datetime } from "$lib/format";
  import { localeFor } from "$lib/locale";

  // Change markers and health events share one axis. That shared axis is the
  // whole point of the app: it is what lets someone see the image that got
  // pulled at 03:00 sitting next to the outage at 03:10.
  //
  // It is also the fastest way to narrow a window: drag across a spike and the
  // feed below follows, which is what onZoom is for.
  let {
    timeline,
    onZoom,
    onReset,
    zoomed = false,
  }: {
    timeline: Timeline | null;
    onZoom?: (from: number, to: number) => void;
    onReset?: () => void;
    zoomed?: boolean;
  } = $props();

  let container = $state<HTMLDivElement | null>(null);
  let chart: uPlot | null = null;

  // 96px was too short to read: a day of activity collapsed into three pixels
  // of bar and the y axis had room for two labels. Taller by default, and
  // taller still on request, because a spike you cannot resolve is a spike you
  // cannot act on.
  const HEIGHTS = { compact: 84, normal: 148, tall: 260 } as const;
  type Size = keyof typeof HEIGHTS;
  let size = $state<Size>("normal");
  const height = $derived(HEIGHTS[size]);

  // Which series are drawn. Clicking a legend entry isolates or restores it,
  // the way every chart people already use behaves.
  let hidden = $state<Record<string, boolean>>({});

  // The hover readout. Rendered as normal DOM beside the canvas rather than
  // painted into it, so it inherits the theme and stays selectable.
  let hover = $state<{
    x: number;
    start: number;
    changes: number;
    events: number;
    errors: number;
  } | null>(null);

  const SERIES = [
    { key: "changes", label: "config changes", stroke: "#10b981" },
    { key: "events", label: "events", stroke: "#8b8b94" },
    { key: "errors", label: "errors", stroke: "#ef4444" },
  ] as const;

  function fill(stroke: string, dark: boolean): string {
    return dark ? `${stroke}55` : `${stroke}44`;
  }

  function series(t: Timeline) {
    const buckets = t.buckets ?? [];
    if (buckets.length === 0) {
      // uPlot needs at least one point; an empty window renders a flat axis
      // rather than an error.
      return [[t.from / 1000, t.to / 1000], [0, 0], [0, 0], [0, 0]];
    }
    // uPlot time scales are in seconds; the API speaks milliseconds.
    const xs = buckets.map((b) => b.start / 1000);
    return [
      xs,
      buckets.map((b) => b.changes),
      buckets.map((b) => b.events - b.errors),
      buckets.map((b) => b.errors),
    ];
  }

  function build(node: HTMLDivElement, t: Timeline, dark: boolean, h: number): uPlot {
    const data = series(t) as uPlot.AlignedData;
    const axis = dark ? "#8b8b94" : "#71717a";
    // A hairline grid rather than a full-strength one: the chart should read as
    // data with a scale behind it, not as a table of boxes.
    const grid = dark ? "#ffffff14" : "#0000000f";

    return new uPlot(
      {
        width: node.clientWidth || 640,
        height: h,
        padding: [8, 4, 0, 0],
        cursor: {
          y: false,
          points: { show: false },
          // Horizontal only: the y axis is a count, and a box selection over
          // counts would suggest a filter Silt does not have.
          drag: { x: true, y: false, setScale: false },
        },
        legend: { show: false },
        // Pin the x range to the window that was actually requested. Left to
        // auto-range, a series with few populated buckets produces an axis
        // spanning years for a one-day window.
        scales: { x: { time: true, range: [t.from / 1000, t.to / 1000] } },
        axes: [
          {
            stroke: axis,
            grid: { stroke: grid, width: 1 },
            ticks: { stroke: grid, size: 4 },
            font: "11px ui-sans-serif, system-ui",
            // uPlot's own time formatting is 12-hour by default, which ignores
            // the reader's preference; these follow it.
            values: (_u, splits) => splits.map((s) => tick(s * 1000)),
          },
          {
            stroke: axis,
            grid: { stroke: grid, width: 1 },
            ticks: { stroke: grid, size: 4 },
            font: "11px ui-sans-serif, system-ui",
            size: 40,
            // Counts are integers; "1.5 errors" is not a thing.
            values: (_u, splits) => splits.map((s) => (Number.isInteger(s) ? String(s) : "")),
          },
        ],
        series: [
          {},
          ...SERIES.map((s) => ({
            label: s.label,
            stroke: s.stroke,
            fill: fill(s.stroke, dark),
            show: !hidden[s.key],
            paths: uPlot.paths.bars!({ size: [0.82, 24] }),
          })),
        ],
        hooks: {
          setCursor: [
            (u) => {
              const idx = u.cursor.idx;
              if (idx == null || u.cursor.left == null || u.cursor.left < 0) {
                hover = null;
                return;
              }
              const bucket = (timeline?.buckets ?? [])[idx];
              if (!bucket) {
                hover = null;
                return;
              }
              hover = {
                x: u.cursor.left,
                start: bucket.start,
                changes: bucket.changes,
                events: bucket.events - bucket.errors,
                errors: bucket.errors,
              };
            },
          ],
          setSelect: [
            (u) => {
              // setScale is off, so uPlot hands us the selection instead of
              // rescaling itself. The window belongs to the page — the feed
              // below has to follow it — so it goes up rather than staying in
              // the chart.
              if (u.select.width <= 4) return;
              const from = u.posToVal(u.select.left, "x") * 1000;
              const to = u.posToVal(u.select.left + u.select.width, "x") * 1000;
              u.setSelect({ left: 0, top: 0, width: 0, height: 0 }, false);
              if (to - from >= 60_000) onZoom?.(Math.round(from), Math.round(to));
            },
          ],
        },
      },
      data,
      node,
    );
  }

  // Axis ticks: time on its own, with the date only where the day turns over.
  function tick(ms: number): string {
    const d = new Date(ms);
    const midnight = d.getHours() === 0 && d.getMinutes() === 0;
    if (midnight) {
      return d.toLocaleDateString(localeFor(undefined), { month: "short", day: "numeric" });
    }
    return d.toLocaleTimeString(localeFor(undefined), {
      hour: "2-digit",
      minute: "2-digit",
      hour12: prefs.clock === "h12" ? true : prefs.clock === "h24" ? false : undefined,
    });
  }

  $effect(() => {
    if (!container || !timeline) return;
    // Reading these here is what makes the canvas repaint on a theme, size or
    // clock change: the axes are drawn pixels, not styled elements.
    const dark = theme.dark;
    const h = height;
    void [hidden, prefs.clock];

    chart?.destroy();
    chart = build(container, timeline, dark, h);
    hover = null;

    const observer = new ResizeObserver(() => {
      if (container && chart) chart.setSize({ width: container.clientWidth, height: h });
    });
    observer.observe(container);

    return () => {
      observer.disconnect();
      chart?.destroy();
      chart = null;
    };
  });

  const totals = $derived.by(() => {
    const buckets = timeline?.buckets ?? [];
    let changes = 0;
    let events = 0;
    let errors = 0;
    for (const b of buckets) {
      changes += b.changes;
      errors += b.errors;
      events += b.events - b.errors;
    }
    return { changes, events, errors };
  });

  function toggle(key: string) {
    // Clicking the only visible series restores the rest, rather than leaving
    // an empty chart with no obvious way back.
    const others = SERIES.filter((s) => s.key !== key);
    const onlyThis = !hidden[key] && others.every((s) => hidden[s.key]);
    hidden = onlyThis ? {} : { ...hidden, [key]: !hidden[key] };
  }
</script>

<div class="rounded-md border border-border bg-card">
  <div class="flex flex-wrap items-center gap-x-4 gap-y-1.5 px-3 py-2 text-xs">
    {#each SERIES as s (s.key)}
      <button
        type="button"
        class="inline-flex items-center gap-1.5 transition-opacity {hidden[s.key]
          ? 'opacity-35'
          : 'opacity-100'} hover:opacity-100"
        onclick={() => toggle(s.key)}
        title="Show only {s.label}"
      >
        <span class="size-2 rounded-[2px]" style="background: {s.stroke}"></span>
        <span class="text-muted-foreground">{s.label}</span>
        <span class="font-mono tabular-nums">
          {s.key === "changes" ? totals.changes : s.key === "errors" ? totals.errors : totals.events}
        </span>
      </button>
    {/each}

    <span class="ml-auto flex items-center gap-2">
      {#if zoomed}
        <button
          type="button"
          class="rounded px-2 py-0.5 text-[11px] text-muted-foreground transition-colors hover:bg-secondary/60 hover:text-foreground"
          onclick={() => onReset?.()}
        >
          Reset zoom
        </button>
      {:else if onZoom}
        <span class="hidden text-[11px] text-muted-foreground/50 sm:inline">drag to zoom</span>
      {/if}
      <div class="flex rounded border border-border">
        {#each [["compact", "S"], ["normal", "M"], ["tall", "L"]] as [value, label] (value)}
          <button
            type="button"
            class="px-1.5 py-0.5 text-[10px] transition-colors first:rounded-l last:rounded-r
                   {size === value ? 'bg-secondary text-secondary-foreground' : 'text-muted-foreground hover:text-foreground'}"
            onclick={() => (size = value as Size)}
            title="{label === 'S' ? 'Compact' : label === 'M' ? 'Normal' : 'Tall'} chart"
          >
            {label}
          </button>
        {/each}
      </div>
    </span>
  </div>

  <div class="relative px-1 pb-1">
    <div
      bind:this={container}
      class="w-full {onZoom ? 'cursor-crosshair' : ''}"
      ondblclick={() => onReset?.()}
      role="presentation"
    ></div>

    {#if hover}
      <!-- Clamped to the container so a bucket at either edge does not push
           the page sideways. -->
      <div
        class="pointer-events-none absolute top-1 z-10 -translate-x-1/2 rounded-md border border-border
               bg-background/95 px-2.5 py-1.5 text-[11px] shadow-lg backdrop-blur-sm"
        style="left: clamp(5rem, {hover.x}px, calc(100% - 5rem))"
      >
        <div class="font-mono text-muted-foreground">{datetime(hover.start)}</div>
        <div class="mt-1 space-y-0.5">
          {#each SERIES as s (s.key)}
            <div class="flex items-center justify-between gap-4">
              <span class="flex items-center gap-1.5 text-muted-foreground">
                <span class="size-1.5 rounded-[2px]" style="background: {s.stroke}"></span>
                {s.label}
              </span>
              <span class="font-mono tabular-nums">
                {s.key === "changes" ? hover.changes : s.key === "errors" ? hover.errors : hover.events}
              </span>
            </div>
          {/each}
        </div>
      </div>
    {/if}
  </div>
</div>
