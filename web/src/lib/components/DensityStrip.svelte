<script lang="ts">
  import uPlot from "uplot";
  import "uplot/dist/uPlot.min.css";
  import type { Timeline } from "$lib/api/client";
  import { Card, CardContent } from "$lib/components/ui/card";

  // Change markers and health events share one axis. That shared axis is the
  // whole point of the app: it is what lets someone see the image that got
  // pulled at 03:00 sitting next to the outage at 03:10.
  let { timeline }: { timeline: Timeline | null } = $props();

  let container = $state<HTMLDivElement | null>(null);
  let chart: uPlot | null = null;

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

  function build(node: HTMLDivElement, t: Timeline): uPlot {
    const data = series(t) as uPlot.AlignedData;
    return new uPlot(
      {
        width: node.clientWidth || 640,
        height: 96,
        cursor: { y: false, points: { show: false } },
        legend: { show: false },
        // Pin the x range to the window that was actually requested. Left to
        // auto-range, a series with few populated buckets produces an axis
        // spanning years for a one-day window.
        scales: {
          x: { time: true, range: [t.from / 1000, t.to / 1000] },
        },
        axes: [
          {
            stroke: "#71717a",
            grid: { stroke: "#27272a", width: 1 },
            ticks: { stroke: "#27272a" },
            font: "11px ui-sans-serif, system-ui",
          },
          {
            stroke: "#71717a",
            grid: { stroke: "#27272a", width: 1 },
            ticks: { stroke: "#27272a" },
            font: "11px ui-sans-serif, system-ui",
            size: 34,
          },
        ],
        series: [
          {},
          { label: "changes", stroke: "#34d399", fill: "rgba(52,211,153,0.25)", paths: uPlot.paths.bars!({ size: [0.7] }) },
          { label: "events", stroke: "#71717a", fill: "rgba(113,113,122,0.2)", paths: uPlot.paths.bars!({ size: [0.7] }) },
          { label: "errors", stroke: "#f87171", fill: "rgba(248,113,113,0.3)", paths: uPlot.paths.bars!({ size: [0.7] }) },
        ],
      },
      data,
      node,
    );
  }

  $effect(() => {
    if (!container || !timeline) return;
    chart?.destroy();
    chart = build(container, timeline);

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
</script>

<Card>
  <CardContent class="p-3">
  <div class="mb-2 flex items-center gap-4 text-xs text-muted-foreground">
    <span class="inline-flex items-center gap-1.5">
      <span class="size-2 rounded-sm bg-emerald-400"></span> config changes
    </span>
    <span class="inline-flex items-center gap-1.5">
      <span class="size-2 rounded-sm bg-zinc-500"></span> events
    </span>
    <span class="inline-flex items-center gap-1.5">
      <span class="size-2 rounded-sm bg-red-400"></span> errors
    </span>
  </div>
  <div bind:this={container} class="w-full"></div>
  </CardContent>
</Card>
