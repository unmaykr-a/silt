<script lang="ts">
  /**
   * A setting that lives in this browser rather than in Silt.
   *
   * Field without the environment half: there is no variable behind a date
   * order, nothing to override, and nothing to reset to.
   */
  import type { Snippet } from "svelte";

  let {
    name,
    label,
    hint,
    children,
  }: { name: string; label: string; hint?: string; children: Snippet } = $props();
</script>

<div class="grid gap-1.5 py-3.5 sm:grid-cols-[15rem_1fr] sm:gap-6">
  <div class="min-w-0">
    <label for={name} class="text-sm font-medium">{label}</label>
    {#if hint}
      <p class="mt-1 text-xs leading-relaxed text-muted-foreground/70">{hint}</p>
    {/if}
  </div>
  <div class="min-w-0 max-w-md">{@render children()}</div>
</div>
