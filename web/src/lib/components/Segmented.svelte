<script lang="ts" generics="T extends string">
  import { markerFor, HIDDEN } from "$lib/marker";
  /**
   * A segmented control whose selection slides between options.
   *
   * Silt had four of these — timeline ranges, project sort, the diff view
   * toggle, theme — each hand-rolled, and each one moved its highlight by
   * simply appearing somewhere else. A marker that jumps gives no sense of
   * which way you went; one that slides makes "I moved one to the right"
   * legible without reading the labels again.
   *
   * The marker is a single absolutely-positioned element measured from the
   * selected button rather than a background on the button itself, because
   * only one element can be animated between two positions.
   */
  let {
    options,
    value = $bindable(),
    label,
    size = "sm",
    onchange,
  }: {
    options: { value: T; label: string; title?: string }[];
    value: T;
    label: string;
    size?: "sm" | "xs";
    // For sources that are not bindable — a store exposing a getter, say.
    onchange?: (next: T) => void;
  } = $props();

  function select(next: T) {
    value = next;
    onchange?.(next);
  }

  let container = $state<HTMLDivElement | null>(null);
  let marker = $state(HIDDEN);

  // Nothing selected hides the marker rather than leaving it on a value no
  // longer in effect — the timeline while a window is dragged out on the strip.
  // markerFor never reads the current value; see web/src/lib/marker.ts.
  function measure() {
    if (!container) return;
    marker = markerFor(container.querySelector<HTMLElement>('[aria-pressed="true"]'));
  }

  // Re-measured on selection and on resize. Fonts loading late change the
  // width of a label, so the first measure can be wrong until they land —
  // hence the ResizeObserver rather than a one-off after mount.
  $effect(() => {
    void value;
    void options;
    measure();
  });

  $effect(() => {
    if (!container || typeof ResizeObserver !== "function") return;
    const observer = new ResizeObserver(() => measure());
    observer.observe(container);
    for (const child of container.children) observer.observe(child);
    return () => observer.disconnect();
  });

  const pad = $derived(size === "xs" ? "px-2 py-1 text-[11px]" : "px-3 py-1.5 text-xs");
</script>

<div
  bind:this={container}
  role="group"
  aria-label={label}
  class="relative isolate flex rounded-md border border-border"
>
  <!-- Hidden until measured: sliding in from 0,0 on first paint reads as a
       glitch rather than as motion. -->
  {#if marker.ready}
    <span
      aria-hidden="true"
      class="absolute inset-y-0.5 -z-10 rounded bg-secondary transition-[left,width] duration-200 ease-out
             motion-reduce:transition-none"
      style="left: {marker.left}px; width: {marker.width}px;"
    ></span>
  {/if}

  {#each options as option (option.value)}
    <button
      type="button"
      onclick={() => select(option.value)}
      aria-pressed={value === option.value}
      title={option.title}
      class="relative flex-1 whitespace-nowrap rounded-md transition-colors {pad}
             {value === option.value
        ? 'text-secondary-foreground'
        : 'text-muted-foreground hover:text-foreground'}"
    >
      {option.label}
    </button>
  {/each}
</div>
