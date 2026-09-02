<script lang="ts">
  /**
   * The mark beside a changelog entry.
   *
   * This was a fixed-width uppercase pill on a coloured background — five
   * words' worth of block down the left edge of every release, which read as
   * the loudest thing on the screen when it is the least interesting. An icon
   * carries the same five categories at a glance and gets out of the way.
   *
   * Drawn rather than typed as emoji: emoji render as a different picture on
   * every platform, cannot take the theme's colour, and would be the only
   * non-vector iconography in an app whose mark, theme control and state dots
   * are all SVG. The colour does the categorising; the shape confirms it, and
   * carries it for anyone who cannot separate the hues.
   */
  let { kind, size = 14 }: { kind: string; size?: number } = $props();

  type Spec = { colour: string; label: string; path: string };

  const SPECS: Record<string, Spec> = {
    added: {
      colour: "text-emerald-600 dark:text-emerald-400",
      label: "Added",
      // A plus.
      path: "M12 5v14M5 12h14",
    },
    changed: {
      colour: "text-sky-600 dark:text-sky-400",
      label: "Changed",
      // Two arrows swapping.
      path: "M17 2l4 4-4 4M21 6H8M7 22l-4-4 4-4M3 18h13",
    },
    fixed: {
      colour: "text-amber-600 dark:text-amber-400",
      label: "Fixed",
      // A wrench.
      path: "M14.7 6.3a4 4 0 0 0 5 5l-9.4 9.4a2.1 2.1 0 0 1-3-3l9.4-9.4a4 4 0 0 0-2-2Z",
    },
    security: {
      colour: "text-red-600 dark:text-red-400",
      label: "Security",
      // A shield.
      path: "M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10Z",
    },
    removed: {
      colour: "text-muted-foreground",
      label: "Removed",
      // A minus.
      path: "M5 12h14",
    },
  };

  const spec = $derived(SPECS[kind] ?? SPECS.removed);
</script>

<span class="inline-flex shrink-0 items-center {spec.colour}" title={spec.label}>
  <svg
    width={size}
    height={size}
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="2"
    stroke-linecap="round"
    stroke-linejoin="round"
    role="img"
    aria-label={spec.label}
  >
    <path d={spec.path} />
  </svg>
</span>
