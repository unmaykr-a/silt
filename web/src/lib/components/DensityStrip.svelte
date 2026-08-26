<script lang="ts">
  import uPlot from "uplot";
  import "uplot/dist/uPlot.min.css";
  import type { Timeline } from "$lib/api/client";
  import { Card, CardContent } from "$lib/components/ui/card";
  import { theme } from "$lib/theme.svelte";

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

  // The hover readout. Rendered as normal DOM beside the canvas rather than
  // painted into it, so it inherits the theme and stays selectable.
  let hover = $state<{ x: number; start: number; changes: number; events: number; errors: number } | null>(null);

  const SERIES = [
    { key: "changes" as const, label: "config changes", stroke: "#34d399", fill: "rgba(52,211,153,0.3)" },
    { key: "events" as const, label: "events", stroke: "#71717a", fill: "rgba(113,113,122,0.25)" },
    { key: "errors" as const, label: "errors", stroke: "#f87171", fill: "rgba(248,113,113,0.35)" },
  ];

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

  function build(node: HTMLDivElement, t: Timeline, dark: boolean): uPlot {
    const data = series(t) as uPlot.AlignedData;
    const axis = dark ? "#71717a" : "#a1a1aa";
    const grid = dark ? "#27272a" : "#e4e4e7";

    return new uPlot(
      {
        width: node.clientWidth || 640,
        height: 96,
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
        scales: {
          x: { time: true, range: [t.from / 1000, t.to / 1000] },
        },
        axes: [
          {
            stroke: axis,
            grid: { stroke: grid, width: 1 },
            ticks: { stroke: grid },
            font: "11px ui-sans-serif, system-ui",
          },
          {
            stroke: axis,
            grid: { stroke: grid, width: 1 },
            ticks: { stroke: grid },
            font: "11px ui-sans-serif, system-ui",
            size: 34,
          },
        ],
        series: [
          {},
          ...SERIES.map((s) => ({
            label: s.label,
            stroke: s.stroke,
            fill: s.fill,
            paths: uPlot.paths.bars!({ size: [0.7] }),
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

  $effect(() => {
    if (!container || !timeline) return;
    // Reading theme.dark here is what makes the canvas repaint on a theme
    // change: the axes are drawn pixels, not styled elements.
    const dark = theme.dark;
    chart?.destroy();
    chart = build(container, timeline, dark);
    hover = null;

    const observer = new ResizeObserver(() => {
      if (container && chart) chart.setSize({ width: container.clientWidth, height: 96 });
    });
    observer.observe(container);

    return () => {
      observer.disconnect();
      chart?.destroy();
      chart = null;
    };
  });

  const stamp = $derived((ms: number) =>
    new Date(ms).toLocaleString(undefined, {
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
    }),
  );
</script>

<Card>
  <CardContent class="p-3">
    <div class="mb-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
      {#each SERIES as s (s.key)}
        <span class="inline-flex items-center gap-1.5">
          <span class="size-2 rounded-sm" style="background: {s.stroke}"></span>
          {s.label}
        </span>
      {/each}

      <span class="ml-auto flex items-center gap-3">
        {#if zoomed}
          <button
            type="button"
            class="rounded border border-border px-2 py-0.5 text-[11px] transition-colors hover:bg-secondary/60 hover:text-foreground"
            onclick={() => onReset?.()}
          >
            Reset zoom
          </button>
        {:else if onZoom}
          <span class="hidden text-[11px] text-muted-foreground/60 sm:inline">drag to zoom</span>
        {/if}
      </span>
    </div>

    <div class="relative">
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
          class="pointer-events-none absolute top-0 z-10 -translate-x-1/2 rounded-md border border-border
                 bg-background/95 px-2.5 py-1.5 text-[11px] shadow-lg backdrop-blur-sm"
          style="left: clamp(4.5rem, {hover.x}px, calc(100% - 4.5rem))"
        >
          <div class="font-mono text-muted-foreground">{stamp(hover.start)}</div>
          <div class="mt-1 space-y-0.5">
            <div class="flex items-center justify-between gap-3">
              <span class="text-muted-foreground">changes</span>
              <span class="font-mono tabular-nums">{hover.changes}</span>
            </div>
            <div class="flex items-center justify-between gap-3">
              <span class="text-muted-foreground">events</span>
              <span class="font-mono tabular-nums">{hover.events}</span>
            </div>
            <div class="flex items-center justify-between gap-3">
              <span class="text-muted-foreground">errors</span>
              <span class="font-mono tabular-nums {hover.errors > 0 ? 'text-red-400' : ''}">{hover.errors}</span>
            </div>
          </div>
        </div>
      {/if}
    </div>
  </CardContent>
</Card>
